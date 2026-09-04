package control

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/assist"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/taskstate"
	"github.com/globulario/sensei-code/internal/workflow"
)

const testWorkspace = "github.com/globulario/sensei-code"

type harness struct {
	t      *testing.T
	server *Server
	http   *httptest.Server
	cred   Credential
	engine *workflow.Engine
	bus    *event.Bus
	store  *session.Store
	root   string
	clock  *testClock
}

type testClock struct{ at time.Time }

func (c *testClock) now() time.Time          { return c.at }
func (c *testClock) advance(d time.Duration) { c.at = c.at.Add(d) }

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := t.TempDir()
	store, err := session.New(root, "sess-1")
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	bus := event.NewBus()
	engine := workflow.New(gitx.Repo{Root: root}, config.Default(), bus, store, "sess-1")

	cred, err := Mint()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	clock := &testClock{at: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)}
	server, err := New(engine, cred, Options{
		Workspace: testWorkspace, LeaseTTL: time.Minute, Now: clock.now,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return &harness{t: t, server: server, http: ts, cred: cred, engine: engine,
		bus: bus, store: store, root: root, clock: clock}
}

type rpcResult struct {
	Result struct {
		Content           []map[string]any `json:"content"`
		StructuredContent map[string]any   `json:"structuredContent"`
		IsError           bool             `json:"isError"`
		Tools             []map[string]any `json:"tools"`
	} `json:"result"`
	Error *rpcError `json:"error"`
}

func (h *harness) post(token, body string) (*http.Response, []byte) {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.http.URL+Endpoint, strings.NewReader(body))
	if err != nil {
		h.t.Fatalf("request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(protocolVersionHeader, SupportedProtocolVersion)
	resp, err := h.http.Client().Do(req)
	if err != nil {
		h.t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatalf("read: %v", err)
	}
	return resp, payload
}

func (h *harness) call(tool string, args map[string]any) rpcResult {
	h.t.Helper()
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	if err != nil {
		h.t.Fatalf("marshal: %v", err)
	}
	_, body := h.post(h.cred.Token(), string(payload))
	var out rpcResult
	if err := json.Unmarshal(body, &out); err != nil {
		h.t.Fatalf("decode %s: %v (%s)", tool, err, body)
	}
	return out
}

// register asks for roles and returns the granted role session for one.
func (h *harness) register(role roles.Role) string {
	h.t.Helper()
	res := h.call("register_role", map[string]any{"roles": []string{string(role)}})
	if res.Error != nil {
		h.t.Fatalf("register %s: %v", role, res.Error.Message)
	}
	granted, _ := res.Result.StructuredContent["granted"].([]any)
	for _, g := range granted {
		m := g.(map[string]any)
		if m["role"] == string(role) {
			return m["role_session"].(string)
		}
	}
	h.t.Fatalf("%s was not granted: %v", role, res.Result.StructuredContent["refused"])
	return ""
}

func (h *harness) writeTask(state taskstate.State) {
	h.t.Helper()
	if err := state.Save(h.root); err != nil {
		h.t.Fatalf("save task: %v", err)
	}
}

// ---------- 1. loopback ----------

func TestTheControlSurfaceBindsLoopbackAndRefusesAnythingElse(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", ":0", "192.0.2.1:0", "8.8.8.8:9000"} {
		if err := requireLoopback(addr); err == nil {
			t.Fatalf("the control surface accepted %q, which is not loopback", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:0", "[::1]:0"} {
		if err := requireLoopback(addr); err != nil {
			t.Fatalf("the control surface refused loopback %q: %v", addr, err)
		}
	}

	h := newHarness(t)
	if err := h.server.Listen("0.0.0.0:0"); err == nil {
		_ = h.server.Close()
		t.Fatal("Listen bound a public interface")
	}
	if err := h.server.Listen("127.0.0.1:0"); err != nil {
		t.Fatalf("Listen refused loopback: %v", err)
	}
	defer h.server.Close()
	if !strings.HasPrefix(h.server.Addr(), "127.0.0.1:") {
		t.Fatalf("bound %s", h.server.Addr())
	}
}

func TestAServerWithoutACredentialOrWorkspaceRefusesToExist(t *testing.T) {
	engine := workflow.New(gitx.Repo{Root: t.TempDir()}, config.Default(), event.NewBus(), nil, "s")
	good, _ := Mint()

	if _, err := New(engine, Credential{}, Options{Workspace: testWorkspace}); err == nil {
		t.Fatal("a control surface came up with no credential")
	}
	if _, err := New(engine, good, Options{}); err == nil {
		t.Fatal("a control surface came up without knowing which repository it serves")
	}
	if _, err := New(nil, good, Options{Workspace: testWorkspace}); err == nil {
		t.Fatal("a control surface came up with nothing canonical to read")
	}
	if _, err := FromToken("   "); err == nil {
		t.Fatal("an empty credential was accepted")
	}
}

// ---------- 2, 3. authentication ----------

func TestAnUnauthenticatedRequestIsRefusedBeforeItIsParsed(t *testing.T) {
	h := newHarness(t)
	// Deliberately malformed. An unauthenticated caller must not learn from the
	// answer whether its request was well formed.
	resp, body := h.post("", "{not json at all")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401; body %s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "JSON") {
		t.Fatalf("an unauthenticated caller was told about its payload: %s", body)
	}
}

func TestAWrongBearerTokenIsRefused(t *testing.T) {
	h := newHarness(t)
	other, _ := Mint()
	for _, token := range []string{other.Token(), "", "  ", h.cred.Token() + "x", h.cred.Token()[:8]} {
		resp, _ := h.post(token, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("token %q reached the surface (status %d)", token, resp.StatusCode)
		}
	}
	resp, _ := h.post(h.cred.Token(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the real token was refused: %d", resp.StatusCode)
	}
}

func TestABearerHeaderIsOnlyAcceptedInItsProperForm(t *testing.T) {
	if got := BearerToken("Bearer abc"); got != "abc" {
		t.Fatalf("BearerToken = %q", got)
	}
	if got := BearerToken("bearer  abc "); got != "abc" {
		t.Fatalf("BearerToken = %q", got)
	}
	for _, header := range []string{"", "abc", "Basic abc", "Bearer", "Bearertoken"} {
		if got := BearerToken(header); got != "" {
			t.Fatalf("BearerToken(%q) = %q, want empty", header, got)
		}
	}
}

// ---------- 4. the token is not durable evidence ----------

func TestTheTokenNeverReachesDurableEventOrSessionOutput(t *testing.T) {
	h := newHarness(t)
	// Drive the whole surface, so anything that would log a credential has had
	// the chance to.
	arch := h.register(roles.Architect)
	h.writeTask(taskstate.State{TaskID: "task-1", Task: "a task", Phase: taskstate.Implementing})
	h.call("get_work", map[string]any{"role_session": arch})
	h.call("inspect_task", map[string]any{"role_session": arch, "task": "task-1"})
	h.call("renew_role", map[string]any{"role_session": arch})
	h.call("release_role", map[string]any{"role_session": arch})
	h.engine.Note("task-1", "a note")

	events, err := h.store.Load()
	if err != nil && !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("load: %v", err)
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	if strings.Contains(string(raw), h.cred.Token()) {
		t.Fatal("the control token was written into the durable session record")
	}

	// And the type itself refuses to render it, however it is reached.
	rendered, err := json.Marshal(map[string]any{"credential": h.cred})
	if err != nil {
		t.Fatalf("marshal credential: %v", err)
	}
	if strings.Contains(string(rendered), h.cred.Token()) {
		t.Fatalf("a marshalled credential disclosed its token: %s", rendered)
	}
	if strings.Contains(h.cred.String(), h.cred.Token()) {
		t.Fatalf("a described credential disclosed its token: %s", h.cred.String())
	}
}

// ---------- 5. authentication is not role authority ----------

func TestAuthenticatingAloneGrantsNoRole(t *testing.T) {
	h := newHarness(t)

	// A valid token reaches the surface.
	resp, _ := h.post(h.cred.Token(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authentication did not reach the surface: %d", resp.StatusCode)
	}
	// And does nothing else. Every task-touching tool refuses without a lease.
	for _, tool := range []string{"get_work", "inspect_task"} {
		args := map[string]any{"role_session": ""}
		if tool == "inspect_task" {
			args["task"] = "task-1"
		}
		res := h.call(tool, args)
		if res.Error == nil {
			t.Fatalf("%s answered an authenticated caller holding no role: %+v", tool, res.Result)
		}
	}
}

// ---------- 6, 7, 8. registration ----------

func TestArchitectRegistrationGrantsOnlyArchitectCapabilities(t *testing.T) {
	h := newHarness(t)
	res := h.call("register_role", map[string]any{"roles": []string{"architect"}})
	if res.Error != nil {
		t.Fatalf("register: %v", res.Error.Message)
	}
	lease := grantedLease(t, res, "architect")
	caps := stringsOf(lease["capabilities"])
	if !contains(caps, "submit_architecture") || !contains(caps, "inspect_task") {
		t.Fatalf("architect capabilities are %v", caps)
	}
	if contains(caps, "submit_review") {
		t.Fatal("the architect role granted submit_review")
	}
	if lease["authority"] != "architectural" {
		t.Fatalf("architect authority is %v", lease["authority"])
	}
}

func TestReviewerRegistrationGrantsOnlyReviewerCapabilities(t *testing.T) {
	h := newHarness(t)
	res := h.call("register_role", map[string]any{"roles": []string{"reviewer"}})
	if res.Error != nil {
		t.Fatalf("register: %v", res.Error.Message)
	}
	lease := grantedLease(t, res, "reviewer")
	caps := stringsOf(lease["capabilities"])
	if !contains(caps, "submit_review") {
		t.Fatalf("reviewer capabilities are %v", caps)
	}
	if contains(caps, "submit_architecture") {
		t.Fatal("the reviewer role granted submit_architecture")
	}
	if lease["authority"] != "execution" {
		t.Fatalf("reviewer authority is %v", lease["authority"])
	}
}

func TestNonLeasableRolesAreRefusedThroughTheRemoteSurface(t *testing.T) {
	h := newHarness(t)
	res := h.call("register_role", map[string]any{
		"roles": []string{"implementer", "counterexample_hunter", "proof_runner", "human", "admin"},
	})
	if res.Error != nil {
		t.Fatalf("register: %v", res.Error.Message)
	}
	granted, _ := res.Result.StructuredContent["granted"].([]any)
	if len(granted) != 0 {
		t.Fatalf("a non-leasable role was granted remotely: %v", granted)
	}
	refused, _ := res.Result.StructuredContent["refused"].([]any)
	if len(refused) != 5 {
		t.Fatalf("refused %d of 5 roles: %v", len(refused), refused)
	}
	for _, r := range refused {
		if strings.TrimSpace(r.(map[string]any)["reason"].(string)) == "" {
			t.Fatalf("a role was refused with no reason: %v", r)
		}
	}
}

// The identity a decision is attributed to comes from the credential. A client
// that could send one would register twice under two names and manufacture the
// appearance of two parties -- which is the thing internal/principal refuses to
// read as independence, one layer up.
func TestAClientCannotSupplyItsOwnPrincipalOrWorkspace(t *testing.T) {
	h := newHarness(t)
	for _, args := range []map[string]any{
		{"roles": []string{"architect"}, "principal": "someone-else"},
		{"roles": []string{"architect"}, "workspace": "github.com/someone/else"},
		{"roles": []string{"architect"}, "authority": "human"},
	} {
		res := h.call("register_role", args)
		if res.Error == nil {
			t.Fatalf("a client supplied %v and was answered: %+v", args, res.Result.StructuredContent)
		}
	}

	// A label is accepted and is not an identity.
	first := h.call("register_role", map[string]any{"roles": []string{"architect"}, "label": "Agent A"})
	second := h.call("register_role", map[string]any{"roles": []string{"architect"}, "label": "Agent B"})
	if first.Result.StructuredContent["principal"] != second.Result.StructuredContent["principal"] {
		t.Fatal("two labels produced two principals")
	}
	if first.Result.StructuredContent["principal"] != string(h.cred.Principal()) {
		t.Fatal("the principal was not derived from the credential")
	}
	if second.Result.StructuredContent["workspace"] != testWorkspace {
		t.Fatalf("workspace is %v", second.Result.StructuredContent["workspace"])
	}
}

func TestTwoCredentialsAreTwoPrincipals(t *testing.T) {
	a, _ := Mint()
	b, _ := Mint()
	if a.Principal() == b.Principal() {
		t.Fatal("two credentials produced one principal")
	}
	again, err := FromToken(a.Token())
	if err != nil {
		t.Fatalf("FromToken: %v", err)
	}
	if again.Principal() != a.Principal() {
		t.Fatal("the same token produced a different principal, so reconnecting would be a new party")
	}
}

// ---------- 9, 10, 11. lease lifecycle ----------

func TestReleaseInvalidatesTheExactLease(t *testing.T) {
	h := newHarness(t)
	arch := h.register(roles.Architect)
	rev := h.register(roles.Reviewer)

	if res := h.call("release_role", map[string]any{"role_session": arch}); res.Error != nil {
		t.Fatalf("release: %v", res.Error.Message)
	}
	if res := h.call("get_work", map[string]any{"role_session": arch}); res.Error == nil {
		t.Fatal("a released role session still worked")
	}
	// And only that one.
	if res := h.call("get_work", map[string]any{"role_session": rev}); res.Error != nil {
		t.Fatalf("releasing one role session broke another: %v", res.Error.Message)
	}
}

func TestExpiryIsDistinguishableFromRelease(t *testing.T) {
	h := newHarness(t)
	vanished := h.register(roles.Architect)
	returned := h.register(roles.Reviewer)
	h.call("release_role", map[string]any{"role_session": returned})

	h.clock.advance(2 * time.Minute)

	expired := h.call("renew_role", map[string]any{"role_session": vanished})
	released := h.call("renew_role", map[string]any{"role_session": returned})
	if !expired.Result.IsError || !released.Result.IsError {
		t.Fatal("a lease that ended was renewed")
	}
	expiredText := refusal(expired)
	releasedText := refusal(released)
	if !strings.Contains(expiredText, "expired") {
		t.Fatalf("a client that stopped answering was told: %s", expiredText)
	}
	if !strings.Contains(releasedText, "released") {
		t.Fatalf("a client that gave the role back was told: %s", releasedText)
	}
	if expiredText == releasedText {
		t.Fatal("a vanished client and a finished one are told the same thing")
	}
}

func TestRenewalCannotWidenOrMoveAuthority(t *testing.T) {
	h := newHarness(t)
	arch := h.register(roles.Architect)
	before := leaseFrom(t, h.call("renew_role", map[string]any{"role_session": arch}), "renewed")

	h.clock.advance(30 * time.Second)
	after := leaseFrom(t, h.call("renew_role", map[string]any{"role_session": arch}), "renewed")

	if after["role"] != before["role"] {
		t.Fatalf("renewal changed the role: %v -> %v", before["role"], after["role"])
	}
	if after["authority"] != before["authority"] {
		t.Fatalf("renewal changed the authority: %v -> %v", before["authority"], after["authority"])
	}
	if len(stringsOf(after["capabilities"])) != len(stringsOf(before["capabilities"])) {
		t.Fatal("renewal changed what the role session grants")
	}
	if after["task"] != before["task"] {
		t.Fatalf("renewal moved the task binding: %v -> %v", before["task"], after["task"])
	}
	if after["expires_at"] == before["expires_at"] {
		t.Fatal("renewal did not extend the lease")
	}
}

func TestARoleSessionHeldByAnotherPrincipalIsRefused(t *testing.T) {
	h := newHarness(t)
	arch := h.register(roles.Architect)

	// A second credential is a second principal, and it may not drive the
	// first one's role session even though it can reach the surface.
	other, _ := Mint()
	server, err := New(h.engine, other, Options{Workspace: testWorkspace, LeaseTTL: time.Minute, Now: h.clock.now})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	// Same registry is deliberately NOT shared here; what is being checked is
	// that a lease id from elsewhere buys nothing.
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()
	stranger := &harness{t: t, server: server, http: ts, cred: other, engine: h.engine, root: h.root, clock: h.clock}
	if res := stranger.call("get_work", map[string]any{"role_session": arch}); res.Error == nil {
		t.Fatal("another principal used a role session it does not hold")
	}
}

// ---------- 12. get_work advances nothing ----------

func TestGetWorkAdvancesNothing(t *testing.T) {
	h := newHarness(t)
	arch := h.register(roles.Architect)
	h.writeTask(taskstate.State{TaskID: "task-1", Task: "a task", Phase: taskstate.Implementing,
		BaseSHA: "abc123", Contract: taskstate.Contract{Plan: "do the thing"}})

	before, foundBefore, err := taskstate.Load(h.root, "task-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	eventsBefore, _ := h.store.Load()

	for i := 0; i < 3; i++ {
		if res := h.call("get_work", map[string]any{"role_session": arch}); res.Error != nil {
			t.Fatalf("get_work: %v", res.Error.Message)
		}
	}

	after, foundAfter, err := taskstate.Load(h.root, "task-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if foundBefore != foundAfter {
		t.Fatal("observing a task changed whether it exists")
	}
	if !sameJSON(t, before, after) {
		t.Fatal("observing a task changed its canonical state")
	}
	eventsAfter, _ := h.store.Load()
	if len(eventsBefore) != len(eventsAfter) {
		t.Fatalf("observing a task wrote %d events into the run record", len(eventsAfter)-len(eventsBefore))
	}
}

// ---------- 13. inspect_task reads canonical state ----------

func TestInspectTaskReadsTheCanonicalRecord(t *testing.T) {
	h := newHarness(t)
	arch := h.register(roles.Architect)
	h.writeTask(taskstate.State{
		TaskID: "task-1", Task: "a task", SessionID: "sess-1", Phase: taskstate.Reviewing,
		BaseSHA: "abc123", Worktree: "/tmp/wt", Branch: "sensei-code/task-1",
		Contract:  taskstate.Contract{Plan: "do the thing", Files: []string{"a.go"}},
		Evidence:  taskstate.Evidence{DiffBytes: 42, AuditVerdict: "pass", ChangedPaths: []string{"a.go"}},
		Open:      []taskstate.Finding{{Source: "reviewer", Detail: "not proven"}},
		Authority: []taskstate.AuthorityDecision{{Question: "q", Chosen: "1", Durable: false}},
		Workers:   []string{"claude"},
	})

	res := h.call("inspect_task", map[string]any{"role_session": arch, "task": "task-1"})
	if res.Error != nil {
		t.Fatalf("inspect_task: %v", res.Error.Message)
	}
	got := res.Result.StructuredContent
	if got["workspace"] != testWorkspace {
		t.Fatalf("workspace is %v", got["workspace"])
	}
	if state(t, got, "workflow_state") != string(assist.Present) {
		t.Fatal("the workflow state was not reported present")
	}
	if text(t, got, "workflow_state") != string(taskstate.Reviewing) {
		t.Fatalf("phase is %v", text(t, got, "workflow_state"))
	}
	if text(t, got, "base") != "abc123" {
		t.Fatalf("base is %v", text(t, got, "base"))
	}
	for _, field := range []string{"candidate", "contract", "authority", "open_findings", "evidence", "workers"} {
		if state(t, got, field) != string(assist.Present) {
			t.Fatalf("%s is %s, want present", field, state(t, got, field))
		}
	}
}

// An empty panel is not an answer. A remote architect that cannot tell "this
// task recorded no contract" from "there is no record of this task" will reason
// confidently about a task it has no evidence for.
func TestMissingInformationIsTypedRatherThanRenderedAsEmptySuccess(t *testing.T) {
	h := newHarness(t)
	arch := h.register(roles.Architect)

	unknown := h.call("inspect_task", map[string]any{"role_session": arch, "task": "never-existed"})
	if unknown.Error != nil {
		t.Fatalf("inspect_task: %v", unknown.Error.Message)
	}
	for _, field := range []string{"record", "workflow_state", "base", "contract", "evidence", "open_findings"} {
		if got := state(t, unknown.Result.StructuredContent, field); got != string(assist.Absent) {
			t.Fatalf("an unknown task reported %s as %s, want absent", field, got)
		}
	}

	// A record that exists and holds no contract is EmptyProven, which is a
	// different fact and the only one of the two a reader may act on.
	h.writeTask(taskstate.State{TaskID: "task-1", Task: "a task", Phase: taskstate.Planning})
	bare := h.call("inspect_task", map[string]any{"role_session": arch, "task": "task-1"})
	for _, field := range []string{"contract", "candidate", "evidence", "open_findings", "authority", "workers", "base"} {
		if got := state(t, bare.Result.StructuredContent, field); got != string(assist.EmptyProven) {
			t.Fatalf("a recorded task with no %s reported %s, want empty-proven", field, got)
		}
	}
	if state(t, bare.Result.StructuredContent, "record") != string(assist.Present) {
		t.Fatal("a task that has a record reported it as something else")
	}
}

// Whether the record's Sensei facts are still current is a question this
// read-only surface does not ask, so it must not be answered.
func TestGraphFreshnessIsReportedUnavailableRatherThanAssumedCurrent(t *testing.T) {
	h := newHarness(t)
	arch := h.register(roles.Architect)
	h.writeTask(taskstate.State{TaskID: "task-1", Phase: taskstate.Planning, GraphBuildCommit: "58c055fbd9d6"})

	res := h.call("inspect_task", map[string]any{"role_session": arch, "task": "task-1"})
	if got := state(t, res.Result.StructuredContent, "graph_generation"); got != string(assist.Unavailable) {
		t.Fatalf("graph generation reported %s, want unavailable", got)
	}
}

// ---------- 14, 16. one canonical task model ----------

// The claim being tested is about the DURABLE model, not about a shared
// pointer. A control process builds its own Engine, bus and session, exactly as
// a TUI process does; what the two share is the repository and the canonical
// task records under it. So this asserts that the remote surface re-reads that
// record rather than answering from anything it kept -- which is what makes a
// local change visible remotely with nothing synchronized, and what would fail
// immediately if this package cached a task.
func TestARecordedMutationIsVisibleRemotelyWithoutASecondStateModel(t *testing.T) {
	h := newHarness(t)
	arch := h.register(roles.Architect)

	// Written the way the engine writes it, to the engine's own root.
	h.writeTask(taskstate.State{TaskID: "task-1", Task: "a task", Phase: taskstate.Implementing})
	first := h.call("inspect_task", map[string]any{"role_session": arch, "task": "task-1"})
	if got := text(t, first.Result.StructuredContent, "workflow_state"); got != string(taskstate.Implementing) {
		t.Fatalf("remote read %q", got)
	}

	// The local side moves the task on. Nothing tells the control surface.
	h.writeTask(taskstate.State{TaskID: "task-1", Task: "a task", Phase: taskstate.Accepted})

	second := h.call("inspect_task", map[string]any{"role_session": arch, "task": "task-1"})
	if got := text(t, second.Result.StructuredContent, "workflow_state"); got != string(taskstate.Accepted) {
		t.Fatalf("the remote reader saw %q after the record moved on; it is answering from a copy", got)
	}

	// And a task recorded after registration appears without anything being
	// synchronized.
	h.writeTask(taskstate.State{TaskID: "task-2", Task: "another", Phase: taskstate.Planning})
	work := h.call("get_work", map[string]any{"role_session": arch})
	if !strings.Contains(mustJSON(t, work.Result.StructuredContent), "task-2") {
		t.Fatal("a task recorded after registration is invisible remotely")
	}
}

func TestDroppedLiveEventsDoNotAlterCanonicalInspection(t *testing.T) {
	h := newHarness(t)
	arch := h.register(roles.Architect)
	h.writeTask(taskstate.State{TaskID: "task-1", Task: "a task", Phase: taskstate.Implementing})

	// A watcher that stopped reading. The bus drops; the record does not.
	stalled, cancel := h.bus.Subscribe(1)
	defer cancel()
	_ = stalled
	for i := 0; i < 50; i++ {
		h.bus.Publish(event.New("sess-1", "task-1", event.SourceSystem, event.Status, "tick", nil))
	}
	if h.bus.Dropped() == 0 {
		t.Fatal("this test proved nothing: no delivery was actually dropped")
	}

	res := h.call("inspect_task", map[string]any{"role_session": arch, "task": "task-1"})
	if got := text(t, res.Result.StructuredContent, "workflow_state"); got != string(taskstate.Implementing) {
		t.Fatalf("inspection returned %q after dropped deliveries", got)
	}
	if state(t, res.Result.StructuredContent, "record") != string(assist.Present) {
		t.Fatal("dropped live events made a present record look otherwise")
	}
}

// ---------- 15. reconnect ----------

func TestReconnectRecoversCurrentTaskStateWithoutEventHistory(t *testing.T) {
	h := newHarness(t)
	arch := h.register(roles.Architect)
	h.writeTask(taskstate.State{TaskID: "task-1", Task: "a task", Phase: taskstate.Reviewing,
		BaseSHA: "abc123", Contract: taskstate.Contract{Plan: "do the thing"}})
	h.call("inspect_task", map[string]any{"role_session": arch, "task": "task-1"})

	// The client goes away. Its lease runs out and the role is reclaimed.
	h.clock.advance(2 * time.Minute)
	if res := h.call("get_work", map[string]any{"role_session": arch}); res.Error == nil {
		t.Fatal("an expired role session still worked")
	}

	// It comes back with the same credential, so it is the same party, and it
	// registers again -- a NEW authority relationship, not the old one revived.
	again := h.register(roles.Architect)
	if again == arch {
		t.Fatal("an expired lease was revived rather than replaced")
	}
	res := h.call("inspect_task", map[string]any{"role_session": again, "task": "task-1"})
	if res.Error != nil {
		t.Fatalf("inspect after reconnect: %v", res.Error.Message)
	}
	got := res.Result.StructuredContent
	if text(t, got, "workflow_state") != string(taskstate.Reviewing) || text(t, got, "base") != "abc123" {
		t.Fatal("a reconnected client could not recover the task's current state")
	}
	// The task is the unit of continuity: nothing above needed a single event.
}

// ---------- 17. malformed input fails closed ----------

func TestMalformedAndUnknownRequestsFailClosed(t *testing.T) {
	h := newHarness(t)
	arch := h.register(roles.Architect)

	for name, body := range map[string]string{
		"not json":         `{"jsonrpc":`,
		"no method":        `{"jsonrpc":"2.0","id":1}`,
		"wrong version":    `{"jsonrpc":"1.0","id":1,"method":"tools/list"}`,
		"unknown method":   `{"jsonrpc":"2.0","id":1,"method":"tools/destroy"}`,
		"unknown tool":     `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_shell","arguments":{}}}`,
		"unknown argument": `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_work","arguments":{"role_session":"x","exec":"rm -rf /"}}}`,
		"trailing content": `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_work","arguments":{"role_session":"x"}}} {"more":1}`,
	} {
		_, raw := h.post(h.cred.Token(), body)
		var out rpcResult
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s: response is not JSON-RPC: %s", name, raw)
		}
		if out.Error == nil {
			t.Fatalf("%s was accepted: %s", name, raw)
		}
	}

	// A well-formed call still works, so the refusals above are about the input.
	if res := h.call("get_work", map[string]any{"role_session": arch}); res.Error != nil {
		t.Fatalf("a well-formed call was refused: %v", res.Error.Message)
	}
}

func TestTheToolSurfaceIsExactlyFiveReadsAndTwoSubmissions(t *testing.T) {
	h := newHarness(t)
	_, raw := h.post(h.cred.Token(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var out rpcResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := map[string]bool{}
	for _, tool := range out.Result.Tools {
		got[tool["name"].(string)] = true
	}
	want := []string{"register_role", "release_role", "renew_role", "get_work", "inspect_task",
		"submit_architecture", "submit_review"}
	for _, name := range want {
		if !got[name] {
			t.Fatalf("tools/list is missing %s: %v", name, got)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("the surface exposes %v; this slice is five reads and two typed submissions", got)
	}
	// Named individually rather than by counting, because the failure worth
	// catching is a specific verb appearing, not the number changing.
	// start_task is named here on purpose: an architect lease is architectural
	// authority, never human objective authority, so holding one must not mean
	// this principal may invent development work and have it executed.
	for _, forbidden := range []string{
		"exec", "run_shell", "write_file", "admit_change", "invoke_worker",
		"advance_task", "complete_task", "start_task", "delegate_task",
	} {
		if got[forbidden] {
			t.Fatalf("the read-only surface exposes %s", forbidden)
		}
	}
}

// ---------- helpers ----------

func grantedLease(t *testing.T, res rpcResult, role string) map[string]any {
	t.Helper()
	granted, _ := res.Result.StructuredContent["granted"].([]any)
	for _, g := range granted {
		m := g.(map[string]any)
		if m["role"] == role {
			return m
		}
	}
	t.Fatalf("%s was not granted: %v", role, res.Result.StructuredContent)
	return nil
}

func leaseFrom(t *testing.T, res rpcResult, key string) map[string]any {
	t.Helper()
	if res.Error != nil {
		t.Fatalf("%s: %v", key, res.Error.Message)
	}
	m, ok := res.Result.StructuredContent[key].(map[string]any)
	if !ok {
		t.Fatalf("no %s in %v", key, res.Result.StructuredContent)
	}
	return m
}

func refusal(res rpcResult) string {
	if s, ok := res.Result.StructuredContent["refused"].(string); ok {
		return s
	}
	return ""
}

func state(t *testing.T, view map[string]any, field string) string {
	t.Helper()
	obs, ok := view[field].(map[string]any)
	if !ok {
		t.Fatalf("%s is not an observation: %v", field, view[field])
	}
	s, _ := obs["state"].(string)
	return s
}

func text(t *testing.T, view map[string]any, field string) string {
	t.Helper()
	obs, ok := view[field].(map[string]any)
	if !ok {
		t.Fatalf("%s is not an observation: %v", field, view[field])
	}
	s, _ := obs["text"].(string)
	return s
}

func stringsOf(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

func sameJSON(t *testing.T, a, b any) bool {
	t.Helper()
	return mustJSON(t, a) == mustJSON(t, b)
}

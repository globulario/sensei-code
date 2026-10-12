package workflow

// N3 witnesses. Every call goes through the REAL transport: the service's
// unix socket, the ExecShim relay the client is launched with, and MCP's
// newline-delimited JSON-RPC framing. Only the client is scripted.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/validation"
)

const (
	n3Task  = "task-1"
	n3World = "world-1"
	n3Base  = "base-1"
	n3Main  = "package main\n\nfunc main() { println(\"broken\") }\n"
	n3Test  = "package pkg\n\nimport \"testing\"\n\nfunc TestP(t *testing.T) {}\n"
)

// n3Wrapper is a stand-in D2 wrapper: it drops its own arguments up to "--"
// and runs the rest. What makes it confinement here is only that the broker
// refuses to run anything except through it, pinned by digest.
const n3Wrapper = "#!/bin/sh\nwhile [ \"$1\" != \"--\" ]; do shift; done\nshift\nexec \"$@\"\n"

type execRig struct {
	t      *testing.T
	e      *Engine
	repo   string
	work   string
	svc    *execService
	events <-chan event.Event
}

func n3Attempt(id string, files ...string) planAttempt {
	return planAttempt{ID: id, TaskID: n3Task, World: n3World, Plan: architectureDecision{Decision: "proceed", Files: files}}
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, body := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// shortTempDir keeps the socket path inside the unix path limit, which a
// test-named t.TempDir can exceed.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "n3")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// pinnedWrapper writes a wrapper script and returns the confinement pinning it.
func pinnedWrapper(t *testing.T, body string) config.Confinement {
	t.Helper()
	p := filepath.Join(t.TempDir(), "d2-confine")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	return config.Confinement{Profile: "d2-test", Command: p, Args: []string{"--profile", "d2-test"}, SHA256: hex.EncodeToString(sum[:])}
}

func newExecRig(t *testing.T, test string, files ...string) *execRig {
	t.Helper()
	repo, work := t.TempDir(), t.TempDir()
	writeFiles(t, work, map[string]string{"main.go": n3Main, "pkg/pkg.go": "package pkg\n", "pkg/pkg_test.go": n3Test})
	store, err := session.New(repo, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{Repo: gitx.Repo{Root: repo}, Config: config.Default(), Bus: event.NewBus(), Store: store, SessionID: "session-1"}
	e.Config.ExecBinding = config.ExecBinding{Enabled: true, RuntimeDir: shortTempDir(t), Confinement: pinnedWrapper(t, n3Wrapper)}
	e.Config.Validation = config.Validation{Test: []config.Command{{Command: "sh", Args: []string{"-c", test}}}}
	if err := (candidate.Identity{TaskID: n3Task, BaseSHA: n3Base, Worktree: work}).Save(repo); err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		files = []string{"main.go", "pkg/pkg_test.go"}
	}
	e.installOperativePlanAttempt(n3Task, n3Attempt("attempt-a", files...))
	events, cancel := e.Bus.Subscribe(4096)
	t.Cleanup(cancel)
	// The task's root, so the session lineage admits its records.
	e.emitIn(fixtureCtx(e, n3Task), event.New(e.SessionID, n3Task, event.SourceUser, event.TaskCreated, "repair main.go", nil))
	if root := drainEvents(events); len(root) != 1 || root[0].Kind != event.TaskCreated {
		t.Fatalf("premise: the task root was not recorded: %+v", root)
	}
	r := &execRig{t: t, e: e, repo: repo, work: work, events: events}
	r.start()
	return r
}

func (r *execRig) start() {
	r.t.Helper()
	svc, err := r.e.startImplementerExec(fixtureCtx(r.e, n3Task), n3Task, r.work, n3Runner)
	if err != nil {
		r.t.Fatalf("the execution service did not start: %v", err)
	}
	r.t.Cleanup(svc.stop)
	r.svc = svc
}

// mcpClient speaks MCP to the service through ExecShim, over pipes standing
// in for the client process's stdio.
type mcpClient struct {
	t    *testing.T
	in   *io.PipeWriter
	out  *bufio.Reader
	next int
	done chan error
}

func dialShim(t *testing.T, socket, service string) *mcpClient {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := ExecShim(context.Background(), socket, service, inR, outW)
		outW.CloseWithError(io.EOF)
		done <- err
	}()
	c := &mcpClient{t: t, in: inW, out: bufio.NewReader(outR), done: done}
	t.Cleanup(func() { inW.Close() })
	return c
}

func (r *execRig) client() *mcpClient {
	r.t.Helper()
	c := dialShim(r.t, r.svc.socket, r.svc.b.Invocation)
	res, err := c.request("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "n3-witness"}})
	if err != nil {
		r.t.Fatalf("initialize: %v", err)
	}
	if info, _ := res["serverInfo"].(map[string]any); info["name"] != agent.ExecServerName {
		r.t.Fatalf("the server is not %s: %v", agent.ExecServerName, res)
	}
	c.notify("notifications/initialized")
	return c
}

func (c *mcpClient) send(v any) error {
	b, _ := json.Marshal(v)
	_, err := c.in.Write(append(b, '\n'))
	return err
}

func (c *mcpClient) notify(method string) {
	_ = c.send(map[string]any{"jsonrpc": "2.0", "method": method})
}

func (c *mcpClient) request(method string, params any) (map[string]any, error) {
	c.next++
	id := c.next
	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	type reply struct {
		line []byte
		err  error
	}
	got := make(chan reply, 1)
	go func() {
		line, err := c.out.ReadBytes('\n')
		got <- reply{line, err}
	}()
	select {
	case r := <-got:
		if r.err != nil {
			return nil, r.err
		}
		var msg struct {
			ID     int            `json:"id"`
			Result map[string]any `json:"result"`
			Error  *rpcError      `json:"error"`
		}
		if err := json.Unmarshal(r.line, &msg); err != nil {
			return nil, err
		}
		if msg.ID != id {
			return nil, fmt.Errorf("reply to %d answered %d", id, msg.ID)
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("rpc error %d: %s", msg.Error.Code, msg.Error.Message)
		}
		return msg.Result, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("no reply to %s", method)
	}
}

// call is one tools/call: its text, and whether it was returned as an error.
func (c *mcpClient) call(name string, args map[string]any) (string, bool) {
	c.t.Helper()
	res, err := c.request("tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		c.t.Fatalf("%s: %v", name, err)
	}
	content, _ := res["content"].([]any)
	if len(content) != 1 {
		c.t.Fatalf("%s returned %v", name, res)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	isErr, _ := res["isError"].(bool)
	return text, isErr
}

func (c *mcpClient) ok(name string, args map[string]any) map[string]any {
	c.t.Helper()
	text, isErr := c.call(name, args)
	if isErr {
		c.t.Fatalf("%s was refused: %s", name, text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		c.t.Fatalf("%s: %v: %s", name, err, text)
	}
	return out
}

func (c *mcpClient) refused(name string, args map[string]any, code string) string {
	c.t.Helper()
	text, isErr := c.call(name, args)
	if !isErr || !strings.HasPrefix(text, code+":") {
		c.t.Fatalf("%s: want %s as an error, got isError=%v %s", name, code, isErr, text)
	}
	return text
}

func fileSHA(t *testing.T, p string) string {
	t.Helper()
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return sha256Hex(body)
}

func (r *execRig) validationRuns() []validation.Bundle {
	var out []validation.Bundle
	for _, ev := range drainEvents(r.events) {
		if ev.Kind != event.ValidationRun {
			continue
		}
		var v validation.Bundle
		if err := json.Unmarshal(ev.Payload, &v); err != nil {
			r.t.Fatalf("a ValidationRun payload does not decode: %v", err)
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		r.e.mu.Lock()
		inv := r.e.admitted[n3Task]
		r.e.mu.Unlock()
		if f := r.e.invocationFailure(inv); f != nil {
			r.t.Logf("the invocation recorded a failure: %v", f)
		}
	}
	return out
}

func (r *execRig) read(c *mcpClient, p string) string {
	r.t.Helper()
	return c.ok("read_file", map[string]any{"path": p})["sha256"].(string)
}

// W1. An admitted repair through the actual three-tool surface: read, patch at
// the preimage, check under the pinned confinement. The evidence is bound to
// the task, to the digest of the candidate the check ran against, to the named
// check, to the confinement and to the implementer as requester, and it is
// advisory. Fails if any tool is missing from the surface, if the patch does
// not land, or if the evidence is bound to anything else.
func TestN3W1AnAdmittedRepairThroughTheRealSurface(t *testing.T) {
	r := newExecRig(t, "grep -q fixed main.go")
	c := r.client()
	tools, err := c.request("tools/list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "read_file,apply_patch,run_check" {
		t.Fatalf("the surface is %v", names)
	}
	got := c.ok("read_file", map[string]any{"path": "main.go", "start_line": 3, "max_lines": 1})
	if got["content"] != "func main() { println(\"broken\") }\n" || got["total_lines"] != float64(3) {
		t.Fatalf("read_file excerpt: %v", got)
	}
	patched := c.ok("apply_patch", map[string]any{"path": "main.go", "expected_sha256": got["sha256"], "old_string": "broken", "new_string": "fixed"})
	if body, _ := os.ReadFile(filepath.Join(r.work, "main.go")); string(body) != strings.Replace(n3Main, "broken", "fixed", 1) {
		t.Fatalf("the patch did not land: %q", body)
	}
	if patched["sha256"] != fileSHA(t, filepath.Join(r.work, "main.go")) {
		t.Fatalf("apply_patch reported %v", patched)
	}
	digest, err := r.svc.candidateDigest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	res := c.ok("run_check", map[string]any{"name": "go_test"})
	results := res["results"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["outcome"] != string(validation.Passed) || res["candidate_digest"] != digest {
		t.Fatalf("run_check: %v", res)
	}
	runs := r.validationRuns()
	if len(runs) != 1 {
		t.Fatalf("expected one ValidationRun, got %d", len(runs))
	}
	v := runs[0]
	ev := v.Checks[0]
	ranUnder := "advisory tool-time check go_test, invocation " + r.svc.b.Invocation + ", plan attempt attempt-a, base " + n3Base
	if !v.ToolTime() || v.CandidateID != n3Task || v.DiffDigest != digest || ev.CandidateID != n3Task || ev.DiffDigest != digest ||
		ev.RequestedBy != execRequestedBy || ev.Command != "sh" || ev.Detail != ranUnder ||
		!strings.Contains(ev.ExecutedBy, "confinement profile d2-test") || !strings.Contains(ev.ExecutedBy, r.e.Config.ExecBinding.Confinement.SHA256) ||
		ev.Outcome != validation.Passed {
		t.Fatalf("the evidence is not bound to this task, candidate, check and confinement: %+v", v)
	}
	// Advisory: what a worker's check produced certifies nothing, even about
	// the exact bytes it ran against.
	if v.Certifies(n3Task, digest) || ev.Certifies(n3Task, digest) {
		t.Fatal("tool-time evidence certified the candidate")
	}
}

// W2. Wrong task: a client naming another service identity is refused at the
// handshake; an argument that names a task or a worktree is refused, never
// ignored; and a service whose task's recorded candidate is no longer the one
// it was bound to refuses every call.
func TestN3W2ATaskCannotBeChosenByTheClient(t *testing.T) {
	r := newExecRig(t, "true")
	wrong := dialShim(t, r.svc.socket, "another-service")
	if err := <-wrong.done; err == nil || !strings.Contains(err.Error(), "is not service") {
		t.Fatalf("a client naming another service was relayed: %v", err)
	}
	c := r.client()
	sum := r.read(c, "main.go")
	c.refused("read_file", map[string]any{"path": "main.go", "task": "task-2"}, refuseArg)
	c.refused("apply_patch", map[string]any{"path": "main.go", "expected_sha256": sum, "old_string": "broken", "new_string": "x",
		"worktree": "/elsewhere"}, refuseArg)
	c.refused("run_check", map[string]any{"name": "go_test", "command": "sh"}, refuseArg)
	if fileSHA(t, filepath.Join(r.work, "main.go")) != sum {
		t.Fatal("a refused call changed the candidate")
	}
	// The task's recorded candidate now names another worktree.
	if err := (candidate.Identity{TaskID: n3Task, BaseSHA: n3Base, Worktree: t.TempDir()}).Save(r.repo); err != nil {
		t.Fatal(err)
	}
	c.refused("read_file", map[string]any{"path": "main.go"}, refuseTaskBinding)
	c.refused("apply_patch", map[string]any{"path": "main.go", "expected_sha256": sum, "old_string": "broken", "new_string": "x"}, refuseTaskBinding)
	c.refused("run_check", map[string]any{"name": "go_test"}, refuseTaskBinding)
	if fileSHA(t, filepath.Join(r.work, "main.go")) != sum || len(r.validationRuns()) != 0 {
		t.Fatal("a call under a rebound task had an effect")
	}
}

// W3. A replan after the service started: the pinned attempt is no longer
// operative, and both effects are refused with nothing written or recorded.
func TestN3W3AReplanMakesTheInvocationsAuthorityStale(t *testing.T) {
	r := newExecRig(t, "true")
	c := r.client()
	sum := r.read(c, "main.go")
	r.e.installOperativePlanAttempt(n3Task, n3Attempt("attempt-b", "main.go"))
	c.refused("apply_patch", map[string]any{"path": "main.go", "expected_sha256": sum, "old_string": "broken", "new_string": "x"}, refusePlanStale)
	c.refused("run_check", map[string]any{"name": "go_test"}, refusePlanStale)
	if fileSHA(t, filepath.Join(r.work, "main.go")) != sum || len(r.validationRuns()) != 0 {
		t.Fatal("a stale attempt had an effect")
	}
}

// W4. A test-edit grant recorded for attempt A does not admit an edit while B
// is operative; the same grant recorded for B does (the control).
func TestN3W4ASupersededTestEditGrantAdmitsNothing(t *testing.T) {
	r := newExecRig(t, "true")
	r.svc.stop()
	b := n3Attempt("attempt-b", "pkg/pkg.go", "pkg/pkg_test.go")
	r.e.installOperativePlanAttempt(n3Task, b)
	grant := func(attempt string) {
		r.e.mu.Lock()
		r.e.planAttemptsOf(n3Task).recordedEdits[attempt] = testEditRecord{PlanAttemptID: attempt, World: n3World,
			Grants: []testEditGrant{{Path: "pkg/pkg_test.go", Covering: "pkg/pkg.go", World: n3World}}}
		r.e.mu.Unlock()
	}
	grant("attempt-a")
	r.start()
	c := r.client()
	sum := r.read(c, "pkg/pkg_test.go")
	args := map[string]any{"path": "pkg/pkg_test.go", "expected_sha256": sum, "old_string": "TestP", "new_string": "TestQ"}
	c.refused("apply_patch", args, refuseGrant)
	if fileSHA(t, filepath.Join(r.work, "pkg/pkg_test.go")) != sum {
		t.Fatal("an edit under a superseded grant was written")
	}
	grant("attempt-b")
	c.ok("apply_patch", args)
}

// W5. Out of scope: an unplanned file, repository metadata, a symlinked file
// or directory, a parent escape, an absolute path, and a file that does not
// exist -- none written, none created.
func TestN3W5OutOfScopePathsAreRefused(t *testing.T) {
	r := newExecRig(t, "true", "main.go", "link.go", "dir/x.go", "new.go", ".git/config")
	writeFiles(t, r.work, map[string]string{"other.go": "package main\n", "real/x.go": "package real\n", ".git/config": "[core]\n"})
	if err := os.Symlink("main.go", filepath.Join(r.work, "link.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(r.work, "dir")); err != nil {
		t.Fatal(err)
	}
	c := r.client()
	patch := func(p, sum string) map[string]any {
		return map[string]any{"path": p, "expected_sha256": sum, "old_string": "package", "new_string": "package"}
	}
	c.refused("apply_patch", patch("other.go", fileSHA(t, filepath.Join(r.work, "other.go"))), refuseGrant)
	c.refused("apply_patch", patch(".git/config", fileSHA(t, filepath.Join(r.work, ".git/config"))), refusePath)
	c.refused("read_file", map[string]any{"path": ".git/config"}, refusePath)
	c.refused("apply_patch", patch("link.go", fileSHA(t, filepath.Join(r.work, "main.go"))), refusePath)
	c.refused("read_file", map[string]any{"path": "link.go"}, refusePath)
	c.refused("apply_patch", patch("dir/x.go", fileSHA(t, filepath.Join(r.work, "real/x.go"))), refusePath)
	c.refused("read_file", map[string]any{"path": "dir/x.go"}, refusePath)
	c.refused("read_file", map[string]any{"path": "../escape.go"}, refusePath)
	c.refused("read_file", map[string]any{"path": "a/../main.go"}, refusePath)
	c.refused("read_file", map[string]any{"path": filepath.Join(r.work, "main.go")}, refusePath)
	c.refused("apply_patch", patch("new.go", sha256Hex(nil)), refusePath)
	if _, err := os.Lstat(filepath.Join(r.work, "new.go")); !os.IsNotExist(err) {
		t.Fatal("apply_patch created a file")
	}
	if l, err := os.Readlink(filepath.Join(r.work, "link.go")); err != nil || l != "main.go" {
		t.Fatal("the symlink was replaced")
	}
	if body, _ := os.ReadFile(filepath.Join(r.work, "main.go")); string(body) != n3Main {
		t.Fatal("a write went through the symlink")
	}
}

// W6. The file changed after it was read: the stale preimage is refused and
// the bytes are the ones the other writer left. An old_string that is not
// exactly one occurrence is refused too.
func TestN3W6AStalePreimageIsRefusedAndNothingIsWritten(t *testing.T) {
	r := newExecRig(t, "true")
	c := r.client()
	sum := r.read(c, "main.go")
	changed := strings.Replace(n3Main, "broken", "changed elsewhere", 1)
	writeFiles(t, r.work, map[string]string{"main.go": changed})
	c.refused("apply_patch", map[string]any{"path": "main.go", "expected_sha256": sum, "old_string": "func", "new_string": "fn"}, refusePatchStale)
	if body, _ := os.ReadFile(filepath.Join(r.work, "main.go")); string(body) != changed {
		t.Fatalf("a stale patch changed the bytes: %q", body)
	}
	fresh := r.read(c, "main.go")
	c.refused("apply_patch", map[string]any{"path": "main.go", "expected_sha256": fresh, "old_string": "n", "new_string": "m"}, refuseArg)
	c.refused("apply_patch", map[string]any{"path": "main.go", "expected_sha256": fresh, "old_string": "absent", "new_string": "m"}, refuseArg)
	if fileSHA(t, filepath.Join(r.work, "main.go")) != fresh {
		t.Fatal("an ambiguous patch was written")
	}
}

// W7. Missing, mismatched or failing confinement refuses the check, and the
// command never ran on the host: the marker it would write does not exist.
func TestN3W7AMissingOrFailedSandboxRunsNothing(t *testing.T) {
	r := newExecRig(t, "touch ran-unconfined")
	c := r.client()
	marker := filepath.Join(r.work, "ran-unconfined")
	pinned := r.e.Config.ExecBinding.Confinement
	cases := map[string]config.Confinement{
		"absent":     {},
		"mismatched": {Profile: pinned.Profile, Command: pinned.Command, Args: pinned.Args, SHA256: strings.Repeat("0", 64)},
		"failing":    pinnedWrapper(t, "#!/bin/sh\nexit 125\n"),
		"missing":    {Profile: pinned.Profile, Command: filepath.Join(t.TempDir(), "gone"), SHA256: pinned.SHA256},
	}
	for name, conf := range cases {
		r.e.Config.ExecBinding.Confinement = conf
		c.refused("run_check", map[string]any{"name": "go_test"}, refuseSandbox)
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("%s: the check ran without its confinement", name)
		}
	}
	// The control: the pinned wrapper runs it.
	r.e.Config.ExecBinding.Confinement = pinned
	c.ok("run_check", map[string]any{"name": "go_test"})
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("premise: the confined check did not run")
	}
}

// W8. A check that prints PASS and exits non-zero is candidate-failure
// evidence: candidate output is not authority.
func TestN3W8PrintedPassWithAFailingExitIsAFailure(t *testing.T) {
	r := newExecRig(t, "echo PASS; echo ok; exit 1")
	c := r.client()
	res := c.ok("run_check", map[string]any{"name": "test"})
	got := res["results"].([]any)[0].(map[string]any)
	if got["outcome"] != string(validation.Failed) || got["exit_status"] != float64(1) {
		t.Fatalf("printed PASS read as a pass: %v", got)
	}
	runs := r.validationRuns()
	if len(runs) != 1 || runs[0].Passed() || runs[0].Checks[0].Outcome != validation.Failed {
		t.Fatalf("the evidence is not a candidate failure: %+v", runs)
	}
}

// W9. A check that changes the candidate while it runs leaves evidence about
// no bytes: invalidated, not a pass, certifying nothing.
func TestN3W9ACandidateChangeDuringTheCheckInvalidatesItsEvidence(t *testing.T) {
	r := newExecRig(t, "echo '// touched' >> main.go")
	c := r.client()
	before, err := r.svc.candidateDigest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	res := c.ok("run_check", map[string]any{"name": "test"})
	if res["invalidated"] == "" {
		t.Fatalf("the change was not seen: %v", res)
	}
	runs := r.validationRuns()
	if len(runs) != 1 {
		t.Fatalf("expected one ValidationRun, got %d", len(runs))
	}
	v := runs[0]
	if !v.ToolTime() || !strings.Contains(v.Checks[0].Detail, "EVIDENCE_INVALIDATED") || v.DiffDigest != "" || v.Passed() || v.Certifies(n3Task, before) ||
		v.Checks[0].Outcome != validation.Errored || v.Checks[0].Certifies(n3Task, before) {
		t.Fatalf("stale evidence still reads as proof: %+v", v)
	}
}

// W10. The invocation ended: the open connection is closed, the socket and
// its directory are gone, a new client cannot connect, and any call is
// refused.
func TestN3W10AnEndedInvocationHoldsNoCapability(t *testing.T) {
	r := newExecRig(t, "true")
	c := r.client()
	sum := r.read(c, "main.go")
	r.svc.stop()
	if _, err := c.request("tools/call", map[string]any{"name": "read_file", "arguments": map[string]any{"path": "main.go"}}); err == nil {
		t.Fatal("a connection outlived its invocation")
	}
	if _, err := os.Stat(r.svc.dir); !os.IsNotExist(err) {
		t.Fatal("the socket directory outlived its invocation")
	}
	late := dialShim(t, r.svc.socket, r.svc.b.Invocation)
	if err := <-late.done; err == nil {
		t.Fatal("a client reached an ended invocation")
	}
	for _, call := range []struct {
		name string
		args map[string]json.RawMessage
	}{
		{"read_file", map[string]json.RawMessage{"path": json.RawMessage(`"main.go"`)}},
		{"apply_patch", map[string]json.RawMessage{"path": json.RawMessage(`"main.go"`), "expected_sha256": json.RawMessage(`"` + sum + `"`),
			"old_string": json.RawMessage(`"broken"`), "new_string": json.RawMessage(`"x"`)}},
		{"run_check", map[string]json.RawMessage{"name": json.RawMessage(`"test"`)}},
	} {
		if _, refusal := r.svc.call(call.name, call.args); refusal == nil || refusal.Code != refuseTaskBinding {
			t.Fatalf("%s after the invocation ended: %v", call.name, refusal)
		}
	}
	if fileSHA(t, filepath.Join(r.work, "main.go")) != sum {
		t.Fatal("an ended invocation wrote")
	}
}

// A5. A replan waits for an effect already revalidated against the attempt it
// replaces: the check completes, and only then is the new attempt installed.
func TestN3APlanReplacementWaitsForAnInFlightEffect(t *testing.T) {
	r := newExecRig(t, "sleep 1")
	c := r.client()
	var wg sync.WaitGroup
	var checked, installed time.Time
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.ok("run_check", map[string]any{"name": "test"})
		checked = time.Now()
	}()
	time.Sleep(300 * time.Millisecond)
	r.e.installOperativePlanAttempt(n3Task, n3Attempt("attempt-b", "main.go"))
	installed = time.Now()
	wg.Wait()
	if installed.Before(checked.Add(-50 * time.Millisecond)) {
		t.Fatalf("the plan was replaced while an effect under the old one was running (installed %v, checked %v)", installed, checked)
	}
	if runs := r.validationRuns(); len(runs) != 1 || !strings.Contains(runs[0].Checks[0].Detail, "plan attempt attempt-a,") {
		t.Fatalf("the in-flight check's evidence is not the old attempt's: %+v", runs)
	}
}

// F4. The production digest covers the whole worktree: a check that writes
// a new untracked file, go.sum, a file the plan does not name, or only a
// mode, invalidates its own evidence. Fails if the digest covers only the
// plan's files (or needs a git capture the worktree does not have).
func TestN3ACheckThatChangesAnythingInTheWorktreeInvalidatesItsEvidence(t *testing.T) {
	for name, script := range map[string]string{
		"a new untracked file": "mkdir -p testdata && echo x > testdata/new.txt",
		"go.sum":               "echo h1 >> go.sum",
		"an unplanned file":    "echo '// x' >> pkg/pkg.go",
		"only a mode":          "chmod +x pkg/pkg.go",
	} {
		t.Run(name, func(t *testing.T) {
			r := newExecRig(t, script)
			c := r.client()
			res := c.ok("run_check", map[string]any{"name": "test"})
			if res["invalidated"] == "" {
				t.Fatalf("the change was not seen: %v", res)
			}
			runs := r.validationRuns()
			if len(runs) != 1 || runs[0].DiffDigest != "" || runs[0].Checks[0].Outcome != validation.Errored {
				t.Fatalf("stale evidence still reads as proof: %+v", runs)
			}
		})
	}
}

// F4. The digest is the tree's, not what its links point at: a symlink is
// recorded by its target and never followed, the repository metadata is
// outside it, and an empty directory is inside it.
func TestN3TheCandidateDigestFollowsNoLinkAndSkipsOnlyGit(t *testing.T) {
	r := newExecRig(t, "true")
	digest := func() string {
		t.Helper()
		d, err := r.svc.candidateDigest(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeFiles(t, filepath.Dir(outside), map[string]string{"outside.txt": "a"})
	link := filepath.Join(r.work, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	d0 := digest()
	writeFiles(t, filepath.Dir(outside), map[string]string{"outside.txt": "b"})
	if digest() != d0 {
		t.Fatal("the digest followed a symlink out of the worktree")
	}
	writeFiles(t, r.work, map[string]string{".git/HEAD": "ref: refs/heads/x\n"})
	if digest() != d0 {
		t.Fatal("the repository metadata is part of the candidate digest")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("elsewhere", link); err != nil {
		t.Fatal(err)
	}
	d1 := digest()
	if d1 == d0 {
		t.Fatal("a retargeted symlink did not change the digest")
	}
	if err := os.Mkdir(filepath.Join(r.work, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if digest() == d1 {
		t.Fatal("a new directory did not change the digest")
	}
}

// toolText is a tools/call result's text and whether it was an error.
func toolText(res map[string]any) (string, bool) {
	content, _ := res["content"].([]any)
	if len(content) != 1 {
		return "", false
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	isErr, _ := res["isError"].(bool)
	return text, isErr
}

// F5. A call that reaches the service while a check runs -- as the check
// itself could, sharing the uid and able to see the socket -- is refused at
// once instead of waiting on the effect it would be part of. The check here
// waits for a release that comes only after the inner calls answer, so a
// queued call is a hang the test detects. Fails if calls wait on the effect.
func TestN3AReentrantCallDuringACheckIsRefusedPromptly(t *testing.T) {
	gate := shortTempDir(t)
	started, release := filepath.Join(gate, "started"), filepath.Join(gate, "release")
	r := newExecRig(t, "touch "+started+"; while [ ! -e "+release+" ]; do sleep 0.05; done")
	outer, inner := r.client(), r.client()
	sum := r.read(inner, "main.go")
	type answer struct {
		res map[string]any
		err error
	}
	checked := make(chan answer, 1)
	go func() {
		res, err := outer.request("tools/call", map[string]any{"name": "run_check", "arguments": map[string]any{"name": "test"}})
		checked <- answer{res, err}
	}()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("premise: the check never started")
		}
	}
	releaseCheck := func() { _ = os.WriteFile(release, nil, 0o600) }
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"read_file", map[string]any{"path": "main.go"}},
		{"apply_patch", map[string]any{"path": "main.go", "expected_sha256": sum, "old_string": "broken", "new_string": "x"}},
		{"run_check", map[string]any{"name": "test"}},
	} {
		got := make(chan answer, 1)
		go func() {
			res, err := inner.request("tools/call", map[string]any{"name": call.name, "arguments": call.args})
			got <- answer{res, err}
		}()
		select {
		case a := <-got:
			text, isErr := toolText(a.res)
			if a.err != nil || !isErr || !strings.HasPrefix(text, refuseInFlight+":") {
				releaseCheck()
				t.Fatalf("%s during a check: want %s, got isError=%v %s (%v)", call.name, refuseInFlight, isErr, text, a.err)
			}
		case <-time.After(5 * time.Second):
			releaseCheck()
			t.Fatalf("%s during a check waited on it instead of being refused", call.name)
		}
	}
	releaseCheck()
	a := <-checked
	if text, isErr := toolText(a.res); a.err != nil || isErr {
		t.Fatalf("the outer check did not complete: %s (%v)", text, a.err)
	}
	if fileSHA(t, filepath.Join(r.work, "main.go")) != sum {
		t.Fatal("a refused call changed the candidate")
	}
	// Once the check has ended, calls are served again.
	r.read(inner, "main.go")
}

// F9. Exactly one occurrence counts overlapping ones: "aa" occurs twice in
// "aaa". Fails if occurrences are counted without overlap.
func TestN3AnOverlappingOldStringIsAmbiguous(t *testing.T) {
	r := newExecRig(t, "true")
	writeFiles(t, r.work, map[string]string{"main.go": "package main\n\n// aaa\n"})
	c := r.client()
	sum := r.read(c, "main.go")
	c.refused("apply_patch", map[string]any{"path": "main.go", "expected_sha256": sum, "old_string": "aa", "new_string": "b"}, refuseArg)
	if fileSHA(t, filepath.Join(r.work, "main.go")) != sum {
		t.Fatal("an ambiguous patch was written")
	}
	c.ok("apply_patch", map[string]any{"path": "main.go", "expected_sha256": sum, "old_string": "aaa", "new_string": "b"})
}

// The socket and the client configuration are outside candidate control: a
// private directory outside the worktree, a 0600 socket, a binding naming
// only the engine's own relay, and the client refusing any socket that is
// not in a private directory of this user.
func TestN3TheSocketAndConfigurationAreOutsideTheCandidate(t *testing.T) {
	r := newExecRig(t, "true")
	if inside(r.svc.dir, r.work) {
		t.Fatal("the service directory is inside the worktree")
	}
	if info, err := os.Stat(r.svc.dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("the service directory is not 0700: %v", err)
	}
	if info, err := os.Lstat(r.svc.socket); err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("the socket is not a 0600 socket: %v", err)
	}
	b := r.svc.client
	if b.Dir != r.svc.dir || strings.Join(b.Args, " ") != "exec-shim --socket "+r.svc.socket+" --service "+r.svc.b.Invocation {
		t.Fatalf("the client binding is not the engine's: %+v", b)
	}
	for _, id := range []string{"cc-plugin-agents-md@builtin", "cc-plugin-plugin-authoring@builtin", "cc-plugin-telemetry@builtin"} {
		if !strings.Contains(string(b.Settings), `"`+id+`":false`) {
			t.Fatalf("the trusted settings do not disable the builtin plugin %s by its canonical id: %s", id, b.Settings)
		}
	}
	// A runtime directory inside the worktree is refused at start.
	r.e.Config.ExecBinding.RuntimeDir = r.work
	if s, err := r.e.startImplementerExec(fixtureCtx(r.e, n3Task), n3Task, r.work, n3Runner); err == nil {
		s.stop()
		t.Fatal("a service was started with its socket inside the worktree")
	}
	// The client refuses a socket whose directory is not private.
	open := shortTempDir(t)
	if err := os.Chmod(open, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkPrivateSocket(filepath.Join(open, "s")); err == nil {
		t.Fatal("a socket in a shared directory was accepted")
	}
	if err := checkPrivateSocket(r.svc.socket); err != nil {
		t.Fatalf("premise: the service's own socket is private: %v", err)
	}
	// And the worktree holds no client configuration.
	if _, err := os.Stat(filepath.Join(r.work, ".sensei-code")); !os.IsNotExist(err) {
		t.Fatal("client configuration was written into the worktree")
	}
}

// The default is unchanged: with the profile off nothing is started and the
// request the implementer receives is exactly the one it always was.
func TestN3TheDefaultLaunchIsUnchanged(t *testing.T) {
	e := &Engine{Config: config.Default()}
	if e.Config.ExecBinding.Enabled {
		t.Fatal("the execution profile is on by default")
	}
	svc, err := e.startImplementerExec(context.Background(), n3Task, t.TempDir(), n3Runner)
	if err != nil || svc != nil {
		t.Fatalf("a service started with the profile off: %v", err)
	}
	req := agent.Request{Role: roles.Implementer, TaskID: n3Task, Prompt: "p"}
	if got := svc.bind(req); got.Exec != nil || got.Prompt != "p" {
		t.Fatalf("the default request was changed: %+v", got)
	}
	svc.stop()
}

// F12. A default-off configuration is saved exactly as before the profile
// existed: no exec_binding key at all. A configured one round-trips.
func TestN3ADefaultOffConfigurationSavesNoExecBinding(t *testing.T) {
	repo := t.TempDir()
	if err := config.Save(repo, config.Default()); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(config.Path(repo))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "exec_binding") {
		t.Fatalf("the default configuration serializes the execution profile:\n%s", body)
	}
	on := config.Default()
	on.ExecBinding = config.ExecBinding{Enabled: true, Confinement: config.Confinement{Profile: "d2", Command: "/usr/bin/d2"}}
	if err := config.Save(repo, on); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !got.ExecBinding.Enabled || got.ExecBinding.Confinement.Command != "/usr/bin/d2" {
		t.Fatalf("a configured profile did not round-trip: %+v", got.ExecBinding)
	}
}

// A service is not started without the authority it would be bound to.
func TestN3NoServiceStartsWithoutABinding(t *testing.T) {
	repo, work := t.TempDir(), t.TempDir()
	e := &Engine{Repo: gitx.Repo{Root: repo}, Config: config.Default(), Bus: event.NewBus(), SessionID: "session-1"}
	e.Config.ExecBinding = config.ExecBinding{Enabled: true, RuntimeDir: shortTempDir(t)}
	if _, err := e.startImplementerExec(fixtureCtx(e, n3Task), n3Task, work, n3Runner); err == nil {
		t.Fatal("a service started with no operative plan attempt")
	}
	e.installOperativePlanAttempt(n3Task, n3Attempt("attempt-a", "main.go"))
	if _, err := e.startImplementerExec(fixtureCtx(e, n3Task), n3Task, work, n3Runner); err == nil {
		t.Fatal("a service started with no recorded candidate")
	}
	if err := (candidate.Identity{TaskID: n3Task, BaseSHA: n3Base, Worktree: t.TempDir()}).Save(repo); err != nil {
		t.Fatal(err)
	}
	if _, err := e.startImplementerExec(fixtureCtx(e, n3Task), n3Task, work, n3Runner); err == nil {
		t.Fatal("a service started on a worktree that is not the candidate's")
	}
}

// n3Runner is the adapter the binding is enforced by: the claude CLI.
var n3Runner = agent.CLI{Name: "claude", Command: "claude"}

// nonCLIRunner stands in for any runner that is not the agent CLI adapter --
// the remote runner, say -- and would ignore req.Exec.
type nonCLIRunner struct{ ran *bool }

func (r nonCLIRunner) Run(context.Context, agent.Request, func(event.Event)) (agent.Result, error) {
	*r.ran = true
	return agent.Result{}, nil
}

// F6. With the binding enabled, a runner that would not enforce it -- any
// runner but the agent CLI, or a CLI provider that cannot be bound -- is
// refused before the service starts, so the implementer is never run. The
// control: the claude CLI starts it. Fails if startImplementerExec does not
// check the concrete runner.
func TestN3ANonEnforcingRunnerIsRefusedBeforeTheServiceStarts(t *testing.T) {
	r := newExecRig(t, "true")
	r.svc.stop()
	runtime := r.e.Config.ExecBinding.RuntimeDir
	ran := false
	var nilCLI *agent.CLI
	for name, runner := range map[string]agent.Runner{
		"a non-CLI runner":   nonCLIRunner{ran: &ran},
		"no runner":          nil,
		"a nil CLI":          nilCLI,
		"an unbindable CLI":  agent.CLI{Name: "codex", Command: "codex"},
		"an unbindable *CLI": &agent.CLI{Name: "stub", Command: "stub"},
	} {
		svc, err := r.e.startImplementerExec(fixtureCtx(r.e, n3Task), n3Task, r.work, runner)
		if err == nil || svc != nil {
			svc.stop()
			t.Fatalf("%s: the execution service started", name)
		}
		if entries, _ := os.ReadDir(runtime); len(entries) != 0 {
			t.Fatalf("%s: a service directory was created: %v", name, entries)
		}
	}
	if ran {
		t.Fatal("the non-enforcing runner was run")
	}
	svc, err := r.e.startImplementerExec(fixtureCtx(r.e, n3Task), n3Task, r.work, &agent.CLI{Name: "claude", Command: "claude"})
	if err != nil {
		t.Fatalf("premise: the claude CLI was refused: %v", err)
	}
	svc.stop()
}

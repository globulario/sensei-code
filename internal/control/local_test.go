package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/workflow"
)

// The objective is the operator's to authorize and the remote role holder's to
// answer questions about. These prove the boundary rather than describe it.

// localHarness is the read harness plus a bound objective channel and a
// recorder standing in for the engine's submission entry.
type localHarness struct {
	*harness
	submitted []string
	taskIDs   []string
}

// operatorPeer is what the kernel reports for a person at a shell: this user,
// a controlling terminal, and a process this orchestrator did not launch.
func operatorPeer(net.Conn) (peer, error) {
	return peer{PID: 4242, UID: uint32(os.Getuid()), GID: uint32(os.Getgid()), Terminal: 34816}, nil
}

func newLocalHarness(t *testing.T) *localHarness {
	t.Helper()
	// The tests below are about the channel, so they run with the kernel
	// reporting an operator. What this project DECIDES about those facts is
	// never substituted -- see TestOnlyAnOperatorMayOriginateAnObjective, and
	// the worker witness, which uses the real kernel and no stub at all.
	return newLocalHarnessWithPeer(t, operatorPeer)
}

// newLocalHarnessWithPeer builds the channel with a stated observation. Passing
// nil means the kernel's own, which is what the worker witness uses.
func newLocalHarnessWithPeer(t *testing.T, observe func(net.Conn) (peer, error)) *localHarness {
	t.Helper()
	h := newHarness(t)
	h.server.peerFor = observe
	lh := &localHarness{harness: h}
	if err := h.server.ListenLocal(h.root); err != nil {
		t.Fatalf("bind the objective channel: %v", err)
	}
	t.Cleanup(func() { h.server.CloseLocal() })
	go h.server.ServeLocal(func(task string) workflow.Submission {
		lh.submitted = append(lh.submitted, task)
		id := "task-" + task
		lh.taskIDs = append(lh.taskIDs, id)
		// The engine's own entry point is what stamps this; the recorder
		// stands in for it and returns what SubmitGovernedLocal returns.
		return workflow.Submission{TaskID: id, Provenance: workflow.SubmittedByLocalOperator}
	})
	return lh
}

// 1. A local objective enters the engine through the ordinary entry point.
func TestALocalObjectiveReachesTheEnginesSubmissionEntry(t *testing.T) {
	h := newLocalHarness(t)

	// Surrounding whitespace is CARRIED, not trimmed. The channel validates
	// that an objective says something; it does not decide what it says.
	const exact = "  repair the parser  "
	accepted, err := SubmitLocalObjective(h.root, exact)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if len(h.submitted) != 1 || h.submitted[0] != exact {
		t.Fatalf("the engine was handed %q, want the exact submitted bytes %q", h.submitted, exact)
	}
	if accepted.TaskID != "task-"+exact {
		t.Fatalf("the task id was not returned: %q", accepted.TaskID)
	}
	if accepted.Workspace != testWorkspace {
		t.Fatalf("workspace is %q", accepted.Workspace)
	}
}

// 2. The provenance is the engine's answer about its own record, not something
// the submitter chose. It says local operator, and it does NOT say a human
// requested it -- local access is authority over this process, not evidence
// that a person typed.
func TestTheObjectivesProvenanceIsStampedAndCannotBeForged(t *testing.T) {
	h := newLocalHarness(t)

	accepted, err := SubmitLocalObjective(h.root, "repair the parser")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if accepted.Provenance != string(workflow.SubmittedByLocalOperator) {
		t.Fatalf("provenance is %q", accepted.Provenance)
	}
	if (workflow.Objective{Provenance: workflow.SubmittedByLocalOperator}).HumanAuthorized() {
		t.Fatal("a local submission claimed a human requested it; local access is not a person typing")
	}
	if accepted.Provenance == string(workflow.RequestedByHuman) {
		t.Fatal("a local submission was recorded as the interactive human's")
	}

	// A submitter naming its own provenance, principal or authority is refused
	// rather than quietly answered as itself.
	for _, forged := range []string{
		`{"task":"x","provenance":"requested by the human with /run"}`,
		`{"task":"x","principal":"remote:abc"}`,
		`{"task":"x","authority":"human"}`,
		`{"task":"x","human":true}`,
	} {
		if err := rawLocal(t, h.root, forged); err == nil {
			t.Fatalf("a submission carrying %s was accepted", forged)
		}
	}
}

// 8. There is no start_task, and no other verb by which a remote client could
// originate work. Asserted on the live tool list, from the wire.
func TestTheRemoteSurfaceStillOriginatesNothing(t *testing.T) {
	h := newLocalHarness(t)
	_, raw := h.post(h.cred.Token(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var out rpcResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := map[string]bool{}
	for _, tool := range out.Result.Tools {
		got[tool["name"].(string)] = true
	}
	for _, forbidden := range []string{"start_task", "submit_task", "delegate_task", "create_task", "run"} {
		if got[forbidden] {
			t.Fatalf("the remote surface exposes %s: an architect lease is architectural authority, "+
				"never human objective authority", forbidden)
		}
	}
	if len(got) != 7 {
		t.Fatalf("the surface exposes %d tools; adding an objective channel must not widen it: %v", len(got), got)
	}

	// And calling one anyway is a refusal, not an approximation.
	for _, name := range []string{"start_task", "submit_task"} {
		res := h.call(name, map[string]any{"task": "do something"})
		if res.Error == nil {
			t.Fatalf("%s was answered: %+v", name, res.Result.StructuredContent)
		}
	}
}

// The mutation control the review asked for, expressed as a property rather
// than a hand-run experiment: if a submit-an-objective verb ever appears on the
// remote surface, this fails.
func TestNoRemoteVerbCanOriginateAnObjective(t *testing.T) {
	source := readSource(t)
	for _, shape := range []string{
		`case "start_task"`, `"name": "start_task"`,
		`case "submit_task"`, `"name": "submit_task"`,
		`case "create_task"`, `"name": "create_task"`,
	} {
		if strings.Contains(source, shape) {
			t.Fatalf("the remote surface can originate work (%s)", shape)
		}
	}
	// The objective entry must not be reachable from the HTTP dispatch at all.
	// It is a different listener, and this is what keeps it one.
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "server.go", nil, 0)
	if err != nil {
		t.Fatalf("parse server.go: %v", err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if name := selectorName(call.Fun); strings.HasSuffix(name, "SubmitGovernedLocal") ||
			strings.HasSuffix(name, "SubmitGoverned") || strings.HasSuffix(name, "SubmitGovernedUnattended") {
			t.Fatalf("server.go calls %s: the HTTP surface must not be able to originate a task", name)
		}
		return true
	})
}

// 10. Exactly one process owns the engine for a repository. A second control
// process is refused rather than taking the channel from the first.
func TestASecondOwnerCannotTakeTheObjectiveChannel(t *testing.T) {
	first := newLocalHarness(t)

	second := newHarness(t)
	// Same repository root, so the same socket path.
	second.root = first.root
	if err := second.server.ListenLocal(first.root); err == nil {
		second.server.CloseLocal()
		t.Fatal("a second control process took the objective channel from a running owner")
	} else if !strings.Contains(err.Error(), "exactly one process may own") {
		t.Fatalf("the refusal does not say why: %v", err)
	}

	// The first owner still has it.
	if _, err := SubmitLocalObjective(first.root, "still mine"); err != nil {
		t.Fatalf("the original owner lost the channel: %v", err)
	}
}

// A socket left behind by a killed process is not a live owner, and must not
// lock the repository out of ever starting one again.
func TestAStaleSocketDoesNotLockOutTheNextOwner(t *testing.T) {
	h := newHarness(t)
	path := LocalSocketPath(h.root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A file where the socket goes, nobody listening: what a kill -9 leaves.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write stale socket: %v", err)
	}
	if err := h.server.ListenLocal(h.root); err != nil {
		t.Fatalf("a stale socket locked out the next owner: %v", err)
	}
	defer h.server.CloseLocal()
}

// The channel is this user's, on this machine. A tunnel forwards a port; it
// does not forward a filesystem.
func TestTheObjectiveChannelIsRestrictedToThisUser(t *testing.T) {
	h := newLocalHarness(t)
	info, err := os.Stat(h.server.LocalAddr())
	if err != nil {
		t.Fatalf("stat the socket: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("the objective channel is mode %o, want 600", perm)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("the objective channel is not a socket: %v", info.Mode())
	}
	// It lives beside the repository's local state, not on the network.
	if !strings.HasSuffix(h.server.LocalAddr(), filepath.Join(".sensei-code", LocalSocketName)) {
		t.Fatalf("the objective channel is at %s", h.server.LocalAddr())
	}
}

// The channel carries an objective and nothing else. No command, no argv, no
// path, no provider: a local command protocol is a shell with extra steps.
func TestTheObjectiveChannelCarriesAnObjectiveAndNothingElse(t *testing.T) {
	h := newLocalHarness(t)

	for name, body := range map[string]string{
		"empty objective":  `{"task":"   "}`,
		"no objective":     `{}`,
		"a command":        `{"task":"x","command":"rm -rf /"}`,
		"an argv":          `{"task":"x","args":["-rf","/"]}`,
		"a provider":       `{"task":"x","worker":"claude"}`,
		"not json":         `{"task":`,
		"trailing content": `{"task":"x"} {"task":"y"}`,
	} {
		if err := rawLocal(t, h.root, body); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	if len(h.submitted) != 0 {
		t.Fatalf("a refused submission still reached the engine: %v", h.submitted)
	}

	// The type's fields are pinned by NAME, not by count.
	//
	// A count was the original guard and it did its job -- it fired the moment
	// Protocol was added. But a count admits any replacement: swapping Protocol
	// for Command keeps the number and loses the property. The closed set is
	// read by membership, so every future field has to be named here and
	// argued for, which is the review this guard exists to force.
	//
	// Protocol is admissible because it says what the CALLER can read. It
	// carries no command, argv, path, provider, provenance or authority, and it
	// cannot change what runs -- only whether the server proceeds at all.
	// Pinned by NAME, not by count. A count admits any replacement: swapping one
	// field for Command keeps the number and loses the property. The closed set
	// is read by membership, so every future field has to be named here.
	//
	// Protocol is deliberately NOT among them. Negotiation describes the
	// channel, not the objective, and putting a version on this message also
	// delayed the compatibility verdict until after readiness -- which is after
	// the proposal path has spent its approval token. It lives on LocalHello.
	for typeName, want := range map[string]map[string]bool{
		"LocalSubmission": {"Task": true},
		"LocalHello":      {"Protocol": true},
	} {
		got := structFields(t, "local.go", typeName)
		if len(got) != len(want) {
			t.Fatalf("%s has fields %v, want exactly %v", typeName, got, keysOf(want))
		}
		for _, f := range got {
			if !want[f] {
				t.Fatalf("%s carries %q; a local command protocol is a shell with extra steps", typeName, f)
			}
		}
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// #165 and its repair. Compatibility is settled BEFORE readiness, because
// readiness is the point after which a caller may do something durable: the
// proposal path spends an at-most-once approval token between readiness and
// submission. A verdict delivered any later leaves that caller in a fail-closed
// state its own error says not to retry.
//
// Two stale-client shapes exist and both must fail ahead of that point.

// The client that predates readiness entirely: it writes its objective first
// and reads the whole connection as ONE JSON value.
func TestAPreNegotiationClientIsRefusedBeforeReadiness(t *testing.T) {
	h := newLocalHarness(t)

	conn, err := net.Dial("unix", LocalSocketPath(h.root))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(`{"task":"repair the parser"}`)); err != nil {
		t.Fatal(err)
	}
	if unix, ok := conn.(*net.UnixConn); ok {
		_ = unix.CloseWrite()
	}
	raw, err := io.ReadAll(io.LimitReader(conn, maxLocalSubmissionBytes))
	if err != nil {
		t.Fatal(err)
	}

	// It gets exactly ONE value, so this client can actually read its own
	// refusal -- the failure it reports is true and specific rather than a
	// parse error about a stream it never expected.
	var refusal struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &refusal); err != nil {
		t.Fatalf("a pre-negotiation client cannot parse its own refusal: %v (%q)", err, raw)
	}
	if refusal.Error == "" {
		t.Fatalf("no refusal was sent: %q", raw)
	}
	if !strings.Contains(refusal.Error, "nothing durable was spent") {
		t.Errorf("the refusal does not tell the caller nothing was spent: %q", refusal.Error)
	}
	if len(h.submitted) != 0 {
		t.Fatalf("a refused client still reached the engine: %v", h.submitted)
	}
}

// The client that knows readiness but not negotiation: it waits to be told it
// may proceed. The server is waiting for a hello, so readiness never comes and
// the caller fails at the handshake -- before BeginApproval, with nothing
// committed. A hang is the cost of refusing to answer an unidentified peer, and
// it is bounded by localDeadline on both ends.
func TestAClientAwaitingReadinessFailsBeforeAnythingDurable(t *testing.T) {
	h := newLocalHarness(t)

	conn, err := net.Dial("unix", LocalSocketPath(h.root))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Shorter than the server's own bound, so the test measures "readiness did
	// not arrive" rather than waiting out localDeadline.
	_ = conn.SetDeadline(time.Now().Add(750 * time.Millisecond))

	var ready map[string]any
	if err := json.NewDecoder(conn).Decode(&ready); err == nil {
		t.Fatalf("readiness was granted to a client that never identified itself: %v", ready)
	}
	if len(h.submitted) != 0 {
		t.Fatalf("a client that never submitted reached the engine: %v", h.submitted)
	}
}

// A client that speaks the handshake but an OLDER version of it.
//
// Distinct from the two above, and the mutation that exposed the gap proves it:
// both of those die at the decode -- one sends an objective where a hello
// belongs, the other sends nothing -- so neither ever reaches the version
// comparison. Only a well-formed hello carrying a stale number does, and that
// is the case a future protocol bump will actually produce.
func TestAWellFormedHelloWithAStaleVersionIsRefusedBeforeReadiness(t *testing.T) {
	h := newLocalHarness(t)

	conn, err := net.Dial("unix", LocalSocketPath(h.root))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(conn).Encode(LocalHello{Protocol: LocalProtocolStreamedReplies - 1}); err != nil {
		t.Fatal(err)
	}

	var reply map[string]any
	if err := json.NewDecoder(conn).Decode(&reply); err != nil {
		t.Fatalf("no answer to a stale hello: %v", err)
	}
	if ready, _ := reply["ready"].(bool); ready {
		t.Fatalf("readiness was granted to a stale protocol: %v", reply)
	}
	msg, _ := reply["error"].(string)
	if msg == "" {
		t.Fatalf("a stale hello was not refused: %v", reply)
	}
	if !strings.Contains(msg, "nothing durable was spent") {
		t.Errorf("the refusal does not tell the caller nothing was spent: %q", msg)
	}
	if len(h.submitted) != 0 {
		t.Fatalf("a refused client still reached the engine: %v", h.submitted)
	}
}

// The mirror case, and the reason the client checks too. A control process that
// predates negotiation sends readiness with no protocol. Believing it would put
// the proposal path past BeginApproval before the mismatch surfaced.
func TestTheClientRefusesAServerThatPredatesNegotiation(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(LocalSocketPath(root)), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", LocalSocketPath(root))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	submissions := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Exactly the old server: readiness immediately, with no protocol, and
		// without reading anything first.
		_ = json.NewEncoder(conn).Encode(map[string]any{"ready": true, "workspace": "old"})
		var in map[string]any
		if json.NewDecoder(conn).Decode(&in) == nil {
			if task, ok := in["task"].(string); ok {
				submissions <- task
			}
		}
	}()

	auth, err := DialLocalObjective(root)
	if err == nil {
		_ = auth.Close()
		t.Fatal("a control process that predates negotiation was accepted")
	}
	if !strings.Contains(err.Error(), "nothing durable was spent") {
		t.Errorf("the refusal does not tell the caller nothing was spent: %v", err)
	}
	select {
	case task := <-submissions:
		t.Fatalf("an objective was sent to an incompatible server: %q", task)
	default:
	}
}

// The positive control. A guard that refuses everything proves nothing, so the
// current pair must negotiate, accept, and commit exactly once.
func TestTheCurrentPairNegotiatesAndCommitsExactlyOnce(t *testing.T) {
	h := newLocalHarness(t)

	accepted, err := SubmitLocalObjective(h.root, "repair the parser")
	if err != nil {
		t.Fatalf("the current client was refused: %v", err)
	}
	if accepted.TaskID == "" {
		t.Fatal("the current client got no task id")
	}
	if len(h.submitted) != 1 || h.submitted[0] != "repair the parser" {
		t.Fatalf("the objective reached the engine as %v", h.submitted)
	}
}

// 9. Reconnecting does not duplicate work. Each submission is one objective;
// the channel holds no queue to replay.
func TestReconnectingDoesNotDuplicateTheObjective(t *testing.T) {
	h := newLocalHarness(t)

	first, err := SubmitLocalObjective(h.root, "repair the parser")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	// A client that connects again and says nothing gets nothing, and leaves
	// nothing behind.
	conn, err := net.Dial("unix", LocalSocketPath(h.root))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.Close()

	if len(h.submitted) != 1 {
		t.Fatalf("reconnecting created %d tasks: %v", len(h.submitted), h.submitted)
	}
	// A second objective is a second task, deliberately: two submissions are
	// two requests, and de-duplicating them would silently drop work somebody
	// asked for twice on purpose.
	second, err := SubmitLocalObjective(h.root, "repair the lexer")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if second.TaskID == first.TaskID {
		t.Fatal("two objectives became one task")
	}
	if len(h.submitted) != 2 {
		t.Fatalf("submitted %v", h.submitted)
	}
}

func TestSubmittingWithNoOwnerRunningSaysHowToStartOne(t *testing.T) {
	root := t.TempDir()
	_, err := SubmitLocalObjective(root, "repair the parser")
	if err == nil {
		t.Fatal("submitting to nobody succeeded")
	}
	if !strings.Contains(err.Error(), "sensei-code control") {
		t.Fatalf("the refusal does not say how to start an owner: %v", err)
	}
	if _, err := SubmitLocalObjective(root, "   "); err == nil {
		t.Fatal("an empty objective was submitted")
	}
}

// 4/7 remain the PR-4 laws, re-asserted here because this slice adds the entry
// that finally reaches them: a task placed locally is one the remote architect
// may be ASKED about, and never one it may originate.
func TestALocallyPlacedTaskIsTheOneTheRemoteRoleIsAskedAbout(t *testing.T) {
	h := newLocalHarness(t)
	session := h.register(roles.Architect)

	// The engine delegates the architect turn for the task the operator placed.
	resolved, err := h.server.Resolve(workflow.RunnerSpec{Role: roles.Architect, TaskID: "task-1"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, remote := resolved.Runner.(*remoteRunner); !remote {
		t.Fatalf("the architect turn resolved to %T", resolved.Runner)
	}
	// And the implementer stays local whatever the remote holds.
	impl, err := h.server.Resolve(workflow.RunnerSpec{Role: roles.Implementer, TaskID: "task-1"})
	if err != nil {
		t.Fatalf("resolve implementer: %v", err)
	}
	if _, remote := impl.Runner.(*remoteRunner); remote {
		t.Fatal("the implementer was routed to the remote surface")
	}
	_ = session
}

// helpers ------------------------------------------------------------------

// rawLocal sends exact bytes on the channel and reports the refusal, if any.
// rawLocal speaks the wire directly, so a malformed message can be sent that
// the typed client would refuse to construct.
//
// It performs the same handshake the real client does: read the verdict for
// this connection, and only then send. An authority refusal arrives instead of
// readiness and is returned as-is, so callers testing malformed BODIES and
// callers testing refused CALLERS both get the error they are asking about.
func rawLocal(t *testing.T, root, body string) error {
	t.Helper()
	conn, err := net.Dial("unix", LocalSocketPath(root))
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	dec := json.NewDecoder(conn)

	// A current-protocol client speaks first. The server settles compatibility
	// from this before it will say anything, so a helper that skipped it would
	// deadlock rather than exercise the validation these callers are about.
	if err := json.NewEncoder(conn).Encode(LocalHello{Protocol: LocalProtocolStreamedReplies}); err != nil {
		return err
	}

	// The authority verdict for this connection, before the objective is sent.
	var ready map[string]any
	if err := dec.Decode(&ready); err != nil {
		return err
	}
	if msg, ok := ready["error"].(string); ok && msg != "" {
		return errors.New(msg)
	}
	if r, ok := ready["ready"].(bool); !ok || !r {
		return errors.New("the channel did not acknowledge readiness")
	}

	if _, err := conn.Write([]byte(body)); err != nil {
		return err
	}
	// End the message, so a truncated one is refused rather than waited on.
	if unix, ok := conn.(*net.UnixConn); ok {
		_ = unix.CloseWrite()
	}
	var reply map[string]any
	if err := dec.Decode(&reply); err != nil {
		return err
	}
	if msg, ok := reply["error"].(string); ok && msg != "" {
		return errors.New(msg)
	}
	return nil
}

// structFields lists a struct's field names from the source, so a claim about
// the shape of the wire message is checked against the type rather than
// remembered.
func structFields(t *testing.T, path, name string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || spec.Name.Name != name {
			return true
		}
		st, ok := spec.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, f := range st.Fields.List {
			for _, id := range f.Names {
				out = append(out, id.Name)
			}
		}
		return false
	})
	if len(out) == 0 {
		t.Fatalf("no struct %s found in %s", name, path)
	}
	return out
}

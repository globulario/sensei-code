package control

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// raw drives the endpoint with exactly the headers a test names, so the
// transport contract can be checked one rule at a time. The harness helper sets
// a well-formed set; this one sets nothing it is not told to.
func (h *harness) raw(method string, headers map[string]string, body string) (*http.Response, []byte) {
	h.t.Helper()
	req, err := http.NewRequest(method, h.http.URL+Endpoint, strings.NewReader(body))
	if err != nil {
		h.t.Fatalf("request: %v", err)
	}
	for k, v := range headers {
		if v == "" {
			continue
		}
		req.Header.Set(k, v)
	}
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

func (h *harness) wellFormed(extra map[string]string) map[string]string {
	out := map[string]string{
		"Authorization":       "Bearer " + h.cred.Token(),
		"Content-Type":        "application/json",
		"Accept":              "application/json, text/event-stream",
		protocolVersionHeader: SupportedProtocolVersion,
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

const listBody = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

// ---------- initialize ----------

func TestInitializeIsNegotiatedRatherThanAssumed(t *testing.T) {
	h := newHarness(t)
	good := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"1"}}}`

	// The handshake carries no version header: the version is what is being
	// negotiated, so requiring it would make initialize impossible.
	headers := h.wellFormed(nil)
	delete(headers, protocolVersionHeader)
	resp, body := h.raw(http.MethodPost, headers, good)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize without the version header: %d %s", resp.StatusCode, body)
	}
	var out struct {
		Result struct {
			ProtocolVersion string         `json:"protocolVersion"`
			Capabilities    map[string]any `json:"capabilities"`
			ServerInfo      map[string]any `json:"serverInfo"`
		} `json:"result"`
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if out.Error != nil {
		t.Fatalf("initialize refused: %v", out.Error.Message)
	}
	if out.Result.ProtocolVersion != SupportedProtocolVersion {
		t.Fatalf("initialize answered %q", out.Result.ProtocolVersion)
	}
	if out.Result.ServerInfo["name"] == nil || out.Result.Capabilities["tools"] == nil {
		t.Fatalf("the handshake did not describe the server: %s", body)
	}
}

// An empty or malformed initialize must not be treated as a successful one.
// "Initialized" is a state the rest of the protocol depends on, and reaching it
// by ignoring the request agrees to terms nobody stated.
func TestAMalformedInitializeIsNotASuccessfulOne(t *testing.T) {
	h := newHarness(t)
	headers := h.wellFormed(nil)
	delete(headers, protocolVersionHeader)

	for name, body := range map[string]string{
		"no params":            `{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		"empty params":         `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		"no version":           `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{},"clientInfo":{"name":"probe","version":"1"}}}`,
		"blank version":        `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"  ","capabilities":{},"clientInfo":{"name":"probe","version":"1"}}}`,
		"no client":            `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{}}}`,
		"unnamed client":       `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"","version":"1"}}}`,
		"foreign field":        `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"p","version":"1"},"trustMe":true}}`,
		"params not an object": `{"jsonrpc":"2.0","id":1,"method":"initialize","params":[1,2,3]}`,
	} {
		_, raw := h.raw(http.MethodPost, headers, body)
		var out struct {
			Result map[string]any `json:"result"`
			Error  *rpcError      `json:"error"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s: %v (%s)", name, err, raw)
		}
		if out.Error == nil {
			t.Fatalf("initialize with %s succeeded: %s", name, raw)
		}
		if out.Result != nil {
			t.Fatalf("initialize with %s returned a result alongside its error: %s", name, raw)
		}
	}
}

// Advertising a version is a claim to implement its semantics. A server that
// echoed whatever it was asked for would be claiming every revision anybody
// names.
func TestTheServerAnswersWithTheRevisionItImplementsRatherThanTheOneAsked(t *testing.T) {
	h := newHarness(t)
	headers := h.wellFormed(nil)
	delete(headers, protocolVersionHeader)
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"probe","version":"1"}}}`

	_, raw := h.raw(http.MethodPost, headers, body)
	var out struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Result.ProtocolVersion == "2024-11-05" {
		t.Fatal("the server echoed a revision it does not implement")
	}
	if out.Result.ProtocolVersion != SupportedProtocolVersion {
		t.Fatalf("the server answered %q", out.Result.ProtocolVersion)
	}
}

// ---------- MCP-Protocol-Version ----------

func TestEveryRequestAfterInitializeMustNameASupportedRevision(t *testing.T) {
	h := newHarness(t)

	// Absent. This server keeps no protocol session, so there is nothing to
	// recover a negotiated version from, and it will not assume a revision it
	// does not implement.
	headers := h.wellFormed(nil)
	delete(headers, protocolVersionHeader)
	resp, body := h.raw(http.MethodPost, headers, listBody)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a request with no %s got %d: %s", protocolVersionHeader, resp.StatusCode, body)
	}

	for _, version := range []string{"2024-11-05", "2025-03-26", "", "nonsense", "2026-01-01"} {
		resp, _ := h.raw(http.MethodPost, h.wellFormed(map[string]string{protocolVersionHeader: version}), listBody)
		if version == "" {
			// An empty header is the same as none.
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("an empty %s got %d", protocolVersionHeader, resp.StatusCode)
			}
			continue
		}
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s %q got %d, want 400", protocolVersionHeader, version, resp.StatusCode)
		}
	}

	resp, _ = h.raw(http.MethodPost, h.wellFormed(nil), listBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the supported revision was refused: %d", resp.StatusCode)
	}
}

func TestAnUnsupportedRevisionOnInitializeIsAlsoRefused(t *testing.T) {
	h := newHarness(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"p","version":"1"}}}`
	resp, _ := h.raw(http.MethodPost, h.wellFormed(map[string]string{protocolVersionHeader: "2024-11-05"}), body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("initialize carrying an unimplemented %s got %d", protocolVersionHeader, resp.StatusCode)
	}
}

// ---------- Origin ----------

// The DNS-rebinding guard. A page in a browser on this machine can reach a
// loopback port; without this the only thing between a visited web page and
// this surface is a token it cannot read, which is true today and is not a
// reason to leave the door open.
func TestAPresentOriginMustBeThisMachine(t *testing.T) {
	h := newHarness(t)
	for _, origin := range []string{
		"http://evil.example", "https://evil.example", "http://127.0.0.1.evil.example",
		"null", "file://", "http://localhost.evil.example",
	} {
		resp, _ := h.raw(http.MethodPost, h.wellFormed(map[string]string{"Origin": origin}), listBody)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("origin %q got %d, want 403", origin, resp.StatusCode)
		}
	}
	for _, origin := range []string{
		"http://localhost:3000", "http://127.0.0.1:8080", "https://127.0.0.1", "http://[::1]:9000",
	} {
		resp, _ := h.raw(http.MethodPost, h.wellFormed(map[string]string{"Origin": origin}), listBody)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("loopback origin %q got %d", origin, resp.StatusCode)
		}
	}
}

// An ordinary server-to-server client sends no Origin, and requiring one would
// refuse every legitimate use of this surface.
func TestAnAbsentOriginIsAllowed(t *testing.T) {
	h := newHarness(t)
	resp, _ := h.raw(http.MethodPost, h.wellFormed(nil), listBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a client with no Origin got %d", resp.StatusCode)
	}
}

// The origin check runs before authentication, because the attack it prevents
// does not depend on holding a credential.
func TestOriginIsCheckedEvenWithoutACredential(t *testing.T) {
	h := newHarness(t)
	headers := h.wellFormed(map[string]string{"Origin": "http://evil.example"})
	delete(headers, "Authorization")
	resp, _ := h.raw(http.MethodPost, headers, listBody)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("an unauthenticated cross-origin request got %d, want 403", resp.StatusCode)
	}
}

// ---------- GET and media types ----------

func TestGetIsRefusedBecauseThereIsNoServerToClientStream(t *testing.T) {
	h := newHarness(t)
	resp, _ := h.raw(http.MethodGet, h.wellFormed(nil), "")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET got %d, want 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); allow != http.MethodPost {
		t.Fatalf("Allow is %q", allow)
	}
	for _, method := range []string{http.MethodDelete, http.MethodPut, http.MethodPatch} {
		resp, _ := h.raw(method, h.wellFormed(nil), listBody)
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s got %d", method, resp.StatusCode)
		}
	}
}

// A POST body that is not declared as JSON is not an MCP message. Treating any
// POST body as one is how a form submission becomes a tool call.
func TestOnlyAJSONBodyIsReadAsAnMCPMessage(t *testing.T) {
	h := newHarness(t)
	for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data"} {
		resp, _ := h.raw(http.MethodPost, h.wellFormed(map[string]string{"Content-Type": ct}), listBody)
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Fatalf("Content-Type %q got %d, want 415", ct, resp.StatusCode)
		}
	}
	for _, ct := range []string{"application/json", "application/json; charset=utf-8"} {
		resp, _ := h.raw(http.MethodPost, h.wellFormed(map[string]string{"Content-Type": ct}), listBody)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Content-Type %q got %d", ct, resp.StatusCode)
		}
	}
}

func TestAClientThatWillNotTakeJSONIsToldSo(t *testing.T) {
	h := newHarness(t)
	resp, _ := h.raw(http.MethodPost, h.wellFormed(map[string]string{"Accept": "text/event-stream"}), listBody)
	if resp.StatusCode != http.StatusNotAcceptable {
		t.Fatalf("an event-stream-only client got %d, want 406", resp.StatusCode)
	}
	for _, accept := range []string{
		"", "application/json", "application/json, text/event-stream", "*/*", "application/*",
	} {
		resp, _ := h.raw(http.MethodPost, h.wellFormed(map[string]string{"Accept": accept}), listBody)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Accept %q got %d", accept, resp.StatusCode)
		}
	}
}

func TestTheAnswerIsDeclaredAsJSON(t *testing.T) {
	h := newHarness(t)
	resp, _ := h.raw(http.MethodPost, h.wellFormed(nil), listBody)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("the answer is declared %q", ct)
	}
}

// ---------- credential strength ----------

// The bearer boundary is worth what the weakest token that can pass through it
// is worth, and an operator-supplied secret is the path where a short one
// arrives.
func TestASuppliedCredentialMustMeetTheStrengthThisServerMints(t *testing.T) {
	for _, weak := range []string{
		"abc", "x", strings.Repeat("a", 63), strings.Repeat("a", 65),
		strings.Repeat("z", 64), strings.Repeat("A", 64), "password", " ",
	} {
		if _, err := FromToken(weak); err == nil {
			t.Fatalf("a %d-character credential %q was accepted", len(weak), weak)
		}
	}

	minted, err := Mint()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if len(minted.Token()) != TokenLength {
		t.Fatalf("a minted token is %d characters", len(minted.Token()))
	}
	rebuilt, err := FromToken(minted.Token())
	if err != nil {
		t.Fatalf("a minted token was not accepted back: %v", err)
	}
	if rebuilt.Principal() != minted.Principal() {
		t.Fatal("rebuilding a credential changed the principal")
	}
}

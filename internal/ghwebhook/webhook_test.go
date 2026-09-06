package ghwebhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Every test here is hermetic: no GitHub, no Globular, no network. The handler
// is exercised through httptest.NewRecorder, and the two tests that need a real
// socket bind loopback with an OS-chosen port.

// The fixed identities this ingress is configured against. Stated, never
// inferred from one another.
const (
	testInstallationID = 159521273
	testRepositoryID   = 1335129805
	testRepository     = "globulario/sensei-code"
	testReviewIssue    = 156
	// The ChatGPT GitHub principal and the Sensei Code bot. Both appear below
	// only as SENDER METADATA on an authenticated delivery.
	chatGPTLogin  = "davecourtois"
	chatGPTUserID = 1697116
	botLogin      = "globulario-sensei-code[bot]"
	botUserID     = 325661868
)

var testSecret = []byte("a-test-webhook-secret-that-is-not-the-real-one")

// recordingSink counts and captures deliveries.
type recordingSink struct {
	mu   sync.Mutex
	got  []IssueCommentDelivery
	fail error
}

func (s *recordingSink) IssueComment(_ context.Context, d IssueCommentDelivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, d)
	return s.fail
}

func (s *recordingSink) deliveries() []IssueCommentDelivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]IssueCommentDelivery(nil), s.got...)
}

// secretFile writes a 0600 secret and returns its path.
func secretFile(t *testing.T, secret []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "webhook-secret")
	if err := os.WriteFile(path, append(secret, '\n'), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	return path
}

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		Addr:           "127.0.0.1:0",
		SecretFile:     secretFile(t, testSecret),
		InstallationID: testInstallationID,
		RepositoryID:   testRepositoryID,
		Repository:     testRepository,
	}
}

func newTestServer(t *testing.T) (*Server, *recordingSink) {
	t.Helper()
	sink := &recordingSink{}
	srv, err := testConfig(t).Server(sink)
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	if srv == nil {
		t.Fatal("a complete configuration produced no server")
	}
	return srv, sink
}

// post drives the handler with explicit headers, so a test can send a body that
// does not match its signature.
func post(t *testing.T, srv *Server, event, delivery, signature string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, Endpoint, bytes.NewReader(body))
	if event != "" {
		req.Header.Set(EventHeader, event)
	}
	if delivery != "" {
		req.Header.Set(DeliveryHeader, delivery)
	}
	if signature != "" {
		req.Header.Set(SignatureHeader, signature)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// signedPost is the ordinary case: the signature is over exactly these bytes.
func signedPost(t *testing.T, srv *Server, event, delivery string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	return post(t, srv, event, delivery, sign(testSecret, body), body)
}

// commentPayload builds an issue_comment body. Each field is separately
// spoilable so a refusal can be attributed to one cause.
type commentPayload struct {
	Action         string
	InstallationID int64
	RepositoryID   int64
	Repository     string
	Issue          int64
	CommentID      int64
	Body           string
	SenderID       int64
	SenderLogin    string
	SenderType     string
}

func defaultComment() commentPayload {
	return commentPayload{
		Action:         "created",
		InstallationID: testInstallationID,
		RepositoryID:   testRepositoryID,
		Repository:     testRepository,
		Issue:          testReviewIssue,
		CommentID:      2200330044,
		Body:           "[sensei-code:webhook-probe]",
		SenderID:       chatGPTUserID,
		SenderLogin:    chatGPTLogin,
		SenderType:     "User",
	}
}

func (c commentPayload) bytes(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"action":       c.Action,
		"installation": map[string]any{"id": c.InstallationID},
		"repository":   map[string]any{"id": c.RepositoryID, "full_name": c.Repository},
		"issue":        map[string]any{"number": c.Issue},
		"comment":      map[string]any{"id": c.CommentID, "body": c.Body},
		"sender":       map[string]any{"id": c.SenderID, "login": c.SenderLogin, "type": c.SenderType},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return raw
}

func body(rec *httptest.ResponseRecorder) string { return rec.Body.String() }

// ---------------------------------------------------------------- method

func TestOnlyPOSTIsAccepted(t *testing.T) {
	srv, sink := newTestServer(t)
	payload := defaultComment().bytes(t)

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodHead} {
		req := httptest.NewRequest(method, Endpoint, bytes.NewReader(payload))
		// Correctly signed, so the refusal can only be about the method.
		req.Header.Set(SignatureHeader, sign(testSecret, payload))
		req.Header.Set(EventHeader, "issue_comment")
		req.Header.Set(DeliveryHeader, "d-1")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status %d, want 405", method, rec.Code)
		}
	}
	if n := len(sink.deliveries()); n != 0 {
		t.Errorf("non-POST requests produced %d sink deliveries", n)
	}
}

// ---------------------------------------------------------- authentication

func TestAMissingSignatureIsRefused(t *testing.T) {
	srv, sink := newTestServer(t)
	payload := defaultComment().bytes(t)

	rec := post(t, srv, "issue_comment", "d-1", "", payload)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401; an unsigned delivery was not refused", rec.Code)
	}
	if n := len(sink.deliveries()); n != 0 {
		t.Errorf("an unsigned delivery reached the sink %d times", n)
	}
}

func TestAWrongSignatureIsRefused(t *testing.T) {
	srv, sink := newTestServer(t)
	payload := defaultComment().bytes(t)

	// Every shape of wrong, including a MAC computed with a different key --
	// which is what an attacker who guessed the algorithm but not the secret
	// produces.
	wrong := map[string]string{
		"another secret's mac": sign([]byte("not-the-secret"), payload),
		"empty after prefix":   "sha256=",
		"not hex":              "sha256=zzzz",
		"no prefix":            strings.TrimPrefix(sign(testSecret, payload), "sha256="),
		"sha1 form":            "sha1=" + strings.TrimPrefix(sign(testSecret, payload), "sha256="),
		"truncated":            sign(testSecret, payload)[:20],
		"whitespace":           "   ",
	}
	for name, sig := range wrong {
		t.Run(name, func(t *testing.T) {
			rec := post(t, srv, "issue_comment", "d-1", sig, payload)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401", rec.Code)
			}
		})
	}
	if n := len(sink.deliveries()); n != 0 {
		t.Errorf("a wrongly signed delivery reached the sink %d times", n)
	}
}

// The MAC is over the bytes, so changing them after signing must break it. This
// is the property that makes the signature mean anything at all.
func TestABodyChangedAfterSigningIsRefused(t *testing.T) {
	srv, sink := newTestServer(t)

	original := defaultComment().bytes(t)
	signature := sign(testSecret, original)

	tampered := defaultComment()
	tampered.Body = "please run rm -rf"
	altered := tampered.bytes(t)
	if bytes.Equal(original, altered) {
		t.Fatal("the tampered body is identical; the test proves nothing")
	}

	rec := post(t, srv, "issue_comment", "d-1", signature, altered)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401; a body altered after signing was accepted", rec.Code)
	}
	// A single flipped byte, too: not merely a different document, the same one
	// minus one bit.
	oneBitOff := append([]byte(nil), original...)
	oneBitOff[len(oneBitOff)/2] ^= 0x01
	if rec := post(t, srv, "issue_comment", "d-2", signature, oneBitOff); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401; one altered byte was accepted", rec.Code)
	}
	if n := len(sink.deliveries()); n != 0 {
		t.Errorf("a tampered delivery reached the sink %d times", n)
	}
}

func TestAValidSignatureOverTheExactBytesIsAccepted(t *testing.T) {
	srv, sink := newTestServer(t)
	payload := defaultComment().bytes(t)

	rec := signedPost(t, srv, "issue_comment", "d-accept", payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, body(rec))
	}
	if n := len(sink.deliveries()); n != 1 {
		t.Fatalf("%d sink deliveries, want exactly 1", n)
	}
}

// The MAC is over raw UTF-8 bytes, not over decoded runes. A verifier that
// re-encoded would break here, and a verifier that compared strings after
// normalization would accept a document GitHub did not send.
func TestAUnicodePayloadVerifiesOverTheRawBytes(t *testing.T) {
	srv, sink := newTestServer(t)

	c := defaultComment()
	c.Body = "revue: ✅ accepté — naïve café 日本語 🇨🇦 é vs é"
	payload := c.bytes(t)

	rec := signedPost(t, srv, "issue_comment", "d-unicode", payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, body(rec))
	}
	got := sink.deliveries()
	if len(got) != 1 {
		t.Fatalf("%d sink deliveries, want 1", len(got))
	}
	if got[0].CommentBody != c.Body {
		t.Errorf("the body was altered in transit:\n got %q\nwant %q", got[0].CommentBody, c.Body)
	}

	// And the composed vs decomposed forms of the same visible text are
	// different bytes, so a signature over one must not verify the other.
	composed := []byte(`{"action":"created","comment":{"body":"café"}}`)
	decomposed := []byte(`{"action":"created","comment":{"body":"café"}}`)
	if rec := post(t, srv, "issue_comment", "d-nfc", sign(testSecret, composed), decomposed); rec.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401; a signature verified across a unicode normalization change", rec.Code)
	}
}

// THE ORDERING PROOF. Malformed JSON with a bad signature must fail as
// authentication, not as JSON. If it ever reports a parse error, the parser ran
// on unauthenticated input.
func TestMalformedJSONWithABadSignatureFailsAsAuthentication(t *testing.T) {
	srv, sink := newTestServer(t)
	garbage := []byte("{this is not json at all")

	for _, sig := range []string{"", "sha256=deadbeef", sign([]byte("wrong-key"), garbage)} {
		rec := post(t, srv, "issue_comment", "d-1", sig, garbage)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status %d, want 401: parsing preceded authentication", rec.Code)
		}
		if strings.Contains(strings.ToLower(body(rec)), "json") {
			t.Fatalf("the refusal mentions JSON, so the parser ran before the signature check: %s", body(rec))
		}
	}
	if n := len(sink.deliveries()); n != 0 {
		t.Errorf("garbage reached the sink %d times", n)
	}
}

// The other half of the same proof: correctly signed garbage DOES reach the
// parser, and is refused there. Together the two pin the boundary's position
// rather than merely its existence.
func TestAValidSignatureOverInvalidJSONIsAParseRefusal(t *testing.T) {
	srv, sink := newTestServer(t)
	garbage := []byte("{this is not json at all")

	rec := signedPost(t, srv, "issue_comment", "d-1", garbage)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
	if !strings.Contains(body(rec), "not valid JSON") {
		t.Errorf("the refusal does not name the parse failure: %s", body(rec))
	}
	if n := len(sink.deliveries()); n != 0 {
		t.Errorf("unparseable JSON reached the sink %d times", n)
	}
}

func TestAMissingDeliveryIDIsRefused(t *testing.T) {
	srv, sink := newTestServer(t)
	payload := defaultComment().bytes(t)

	for _, id := range []string{"", "   "} {
		rec := post(t, srv, "issue_comment", id, sign(testSecret, payload), payload)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("delivery id %q: status %d, want 400", id, rec.Code)
		}
	}
	if n := len(sink.deliveries()); n != 0 {
		t.Errorf("a delivery with no id reached the sink %d times", n)
	}
}

// ------------------------------------------------------------------- ping

func TestASignedPingIsAcceptedWithNoSinkEffect(t *testing.T) {
	srv, sink := newTestServer(t)
	payload := []byte(`{"zen":"Non-blocking is better than blocking.","hook_id":1}`)

	rec := signedPost(t, srv, "ping", "d-ping", payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, body(rec))
	}
	if !strings.Contains(body(rec), `"status":"accepted"`) {
		t.Errorf("the receipt does not accept: %s", body(rec))
	}
	if n := len(sink.deliveries()); n != 0 {
		t.Errorf("a ping produced %d sink deliveries; it must have no effect", n)
	}
}

func TestAnUnsignedPingIsRefused(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := post(t, srv, "ping", "d-ping", "", []byte(`{"zen":"x"}`))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401; ping is not exempt from authentication", rec.Code)
	}
}

// --------------------------------------------------------- issue_comment

func TestACreatedCommentForTheExactBindingIsDeliveredOnce(t *testing.T) {
	srv, sink := newTestServer(t)
	c := defaultComment()
	payload := c.bytes(t)

	rec := signedPost(t, srv, "issue_comment", "12345678-90ab-cdef-1234-567890abcdef", payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, body(rec))
	}

	got := sink.deliveries()
	if len(got) != 1 {
		t.Fatalf("%d sink deliveries, want exactly 1", len(got))
	}
	d := got[0]

	// Every normalized field, checked against what was sent.
	for _, f := range []struct {
		name      string
		got, want any
	}{
		{"DeliveryID", d.DeliveryID, "12345678-90ab-cdef-1234-567890abcdef"},
		{"Action", d.Action, "created"},
		{"InstallationID", d.InstallationID, int64(testInstallationID)},
		{"RepositoryID", d.RepositoryID, int64(testRepositoryID)},
		{"RepositoryFullName", d.RepositoryFullName, testRepository},
		{"IssueNumber", d.IssueNumber, int64(testReviewIssue)},
		{"CommentID", d.CommentID, c.CommentID},
		{"CommentBody", d.CommentBody, c.Body},
		{"SenderID", d.SenderID, int64(chatGPTUserID)},
		{"SenderLogin", d.SenderLogin, chatGPTLogin},
		{"SenderType", d.SenderType, "User"},
	} {
		if f.got != f.want {
			t.Errorf("%s = %v, want %v", f.name, f.got, f.want)
		}
	}

	// Both replay keys survive normalization. Neither is CHECKED here -- see
	// the replay boundary note in delivery.go -- but a later slice cannot add
	// durable idempotency for a field the transport threw away.
	if d.DeliveryID == "" || d.CommentID == 0 {
		t.Error("the replay boundary keys did not survive into the normalized event")
	}
}

func TestAMismatchedBindingIsRefused(t *testing.T) {
	cases := map[string]func(*commentPayload){
		"wrong installation id":   func(c *commentPayload) { c.InstallationID = 999 },
		"absent installation id":  func(c *commentPayload) { c.InstallationID = 0 },
		"wrong repository id":     func(c *commentPayload) { c.RepositoryID = 42 },
		"absent repository id":    func(c *commentPayload) { c.RepositoryID = 0 },
		"wrong repository name":   func(c *commentPayload) { c.Repository = "attacker/sensei-code" },
		"absent repository name":  func(c *commentPayload) { c.Repository = "" },
		"right name wrong id":     func(c *commentPayload) { c.RepositoryID = 1 },
		"right id wrong name":     func(c *commentPayload) { c.Repository = "globulario/other" },
		"case-shifted repository": func(c *commentPayload) { c.Repository = "Globulario/Sensei-Code" },
	}

	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			srv, sink := newTestServer(t)
			c := defaultComment()
			spoil(&c)
			payload := c.bytes(t)

			// Correctly signed. The delivery is authentic and still refused:
			// authenticity is not entitlement to be about this repository.
			rec := signedPost(t, srv, "issue_comment", "d-1", payload)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status %d, want 403: %s", rec.Code, body(rec))
			}
			if n := len(sink.deliveries()); n != 0 {
				t.Errorf("a mismatched delivery reached the sink %d times", n)
			}
		})
	}
}

func TestAStructurallyIncompletePayloadIsRefused(t *testing.T) {
	cases := map[string]func(*commentPayload){
		"no issue number": func(c *commentPayload) { c.Issue = 0 },
		"no comment id":   func(c *commentPayload) { c.CommentID = 0 },
		"no sender id":    func(c *commentPayload) { c.SenderID = 0 },
		"no sender login": func(c *commentPayload) { c.SenderLogin = "  " },
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			srv, sink := newTestServer(t)
			c := defaultComment()
			spoil(&c)

			rec := signedPost(t, srv, "issue_comment", "d-1", c.bytes(t))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", rec.Code, body(rec))
			}
			if n := len(sink.deliveries()); n != 0 {
				t.Errorf("an incomplete payload reached the sink %d times", n)
			}
		})
	}
}

// The transport authenticates REPOSITORY events. Which issue is the review
// mailbox is a later protocol decision, so an issue other than 156 must still
// be accepted here.
func TestAnotherIssueNumberIsStillAcceptedByTheTransport(t *testing.T) {
	srv, sink := newTestServer(t)
	c := defaultComment()
	c.Issue = 9001

	rec := signedPost(t, srv, "issue_comment", "d-other-issue", c.bytes(t))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; the transport hardcoded an issue number: %s", rec.Code, body(rec))
	}
	got := sink.deliveries()
	if len(got) != 1 {
		t.Fatalf("%d sink deliveries, want 1", len(got))
	}
	if got[0].IssueNumber != 9001 {
		t.Errorf("IssueNumber = %d, want 9001", got[0].IssueNumber)
	}
}

func TestAnotherActionIsAuthenticatedAndIgnored(t *testing.T) {
	for _, action := range []string{"edited", "deleted", "", "CREATED"} {
		t.Run("action="+action, func(t *testing.T) {
			srv, sink := newTestServer(t)
			c := defaultComment()
			c.Action = action

			rec := signedPost(t, srv, "issue_comment", "d-1", c.bytes(t))
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want 200 (authenticated and ignored): %s", rec.Code, body(rec))
			}
			if n := len(sink.deliveries()); n != 0 {
				t.Errorf("action %q produced %d sink deliveries, want 0", action, n)
			}
		})
	}
}

func TestAnotherEventIsAuthenticatedAndIgnored(t *testing.T) {
	for _, event := range []string{"push", "pull_request", "issues", "installation", "star", ""} {
		t.Run("event="+event, func(t *testing.T) {
			srv, sink := newTestServer(t)
			payload := defaultComment().bytes(t)

			rec := signedPost(t, srv, event, "d-1", payload)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want 200 (authenticated and ignored): %s", rec.Code, body(rec))
			}
			if n := len(sink.deliveries()); n != 0 {
				t.Errorf("event %q produced %d sink deliveries, want 0", event, n)
			}
		})
	}
}

// An unsigned delivery for an event this ingress ignores must still be refused
// as unauthenticated. "We were going to ignore it anyway" is not a reason to
// skip authentication -- the response would then tell an unauthenticated caller
// which events reach a handler.
func TestAnIgnoredEventStillRequiresASignature(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := post(t, srv, "push", "d-1", "", []byte(`{}`))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
}

// ------------------------------------------------------------- sender is not authority

// The two identities that will matter later appear here only as metadata. A
// delivery from either is accepted on its SIGNATURE, and carries no more
// entitlement than a delivery from anyone else with write access.
func TestTheSenderIsMetadataAndNotAuthority(t *testing.T) {
	senders := []struct {
		name  string
		id    int64
		login string
		kind  string
	}{
		{"chatgpt principal", chatGPTUserID, chatGPTLogin, "User"},
		{"sensei-code bot", botUserID, botLogin, "Bot"},
		{"an unrelated collaborator", 5550001, "someone-else", "User"},
	}
	for _, s := range senders {
		t.Run(s.name, func(t *testing.T) {
			srv, sink := newTestServer(t)
			c := defaultComment()
			c.SenderID, c.SenderLogin, c.SenderType = s.id, s.login, s.kind

			rec := signedPost(t, srv, "issue_comment", "d-1", c.bytes(t))
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want 200: %s", rec.Code, body(rec))
			}
			got := sink.deliveries()
			if len(got) != 1 {
				t.Fatalf("%d sink deliveries, want 1", len(got))
			}
			// Recorded exactly, and nothing about the outcome differs by
			// sender: all three take the identical path to the identical sink.
			if got[0].SenderID != s.id || got[0].SenderLogin != s.login || got[0].SenderType != s.kind {
				t.Errorf("sender recorded as %d/%s/%s, want %d/%s/%s",
					got[0].SenderID, got[0].SenderLogin, got[0].SenderType, s.id, s.login, s.kind)
			}
		})
	}

	// And the converse: a correctly-named sender does NOT substitute for a
	// signature. This is the "sender.login is not authentication" pin.
	srv, sink := newTestServer(t)
	c := defaultComment()
	c.SenderID, c.SenderLogin = chatGPTUserID, chatGPTLogin
	if rec := post(t, srv, "issue_comment", "d-1", "", c.bytes(t)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401; a known sender login substituted for a signature", rec.Code)
	}
	if n := len(sink.deliveries()); n != 0 {
		t.Errorf("an unsigned delivery from a known sender reached the sink %d times", n)
	}
}

// ------------------------------------------------------------------- size

func TestAnOversizeBodyIsRefusedWith413(t *testing.T) {
	srv, sink := newTestServer(t)

	c := defaultComment()
	c.Body = strings.Repeat("A", int(MaxBodyBytes)+4096)
	payload := c.bytes(t)
	if int64(len(payload)) <= MaxBodyBytes {
		t.Fatalf("the oversize payload is only %d bytes; the test proves nothing", len(payload))
	}

	// Correctly signed, so the refusal is about size and nothing else -- and
	// note the body is refused before the signature is even considered.
	rec := signedPost(t, srv, "issue_comment", "d-big", payload)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413", rec.Code)
	}
	if n := len(sink.deliveries()); n != 0 {
		t.Errorf("an oversize delivery reached the sink %d times", n)
	}
}

func TestABodyAtTheLimitIsStillRead(t *testing.T) {
	srv, sink := newTestServer(t)

	// A realistic maximum comment: GitHub's own cap is 65536 characters.
	c := defaultComment()
	c.Body = strings.Repeat("x", 65536)
	payload := c.bytes(t)
	if int64(len(payload)) > MaxBodyBytes {
		t.Fatalf("a maximum-length GitHub comment (%d bytes) exceeds MaxBodyBytes; the limit is too low", len(payload))
	}

	if rec := signedPost(t, srv, "issue_comment", "d-max", payload); rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: a maximum-length comment was refused", rec.Code)
	}
	if n := len(sink.deliveries()); n != 1 {
		t.Errorf("%d sink deliveries, want 1", n)
	}
}

// -------------------------------------------------------------- no leakage

// The secret must not appear in any response, at any status, for any request.
func TestTheSecretNeverAppearsInAResponse(t *testing.T) {
	srv, _ := newTestServer(t)
	payload := defaultComment().bytes(t)
	validMAC := sign(testSecret, payload)
	wrongMAC := sign([]byte("wrong-key"), payload)

	responses := []*httptest.ResponseRecorder{
		post(t, srv, "issue_comment", "d-1", "", payload),
		post(t, srv, "issue_comment", "d-1", wrongMAC, payload),
		post(t, srv, "issue_comment", "d-1", "sha256=nothex", payload),
		post(t, srv, "issue_comment", "", validMAC, payload),
		post(t, srv, "issue_comment", "d-1", validMAC, []byte("{bad json")),
		signedPost(t, srv, "ping", "d-1", []byte(`{"zen":"x"}`)),
		signedPost(t, srv, "issue_comment", "d-1", payload),
	}

	forbidden := map[string]string{
		"the secret":     string(testSecret),
		"the valid MAC":  strings.TrimPrefix(validMAC, "sha256="),
		"a supplied MAC": strings.TrimPrefix(wrongMAC, "sha256="),
	}
	for i, rec := range responses {
		for name, needle := range forbidden {
			if strings.Contains(rec.Body.String(), needle) {
				t.Errorf("response %d leaks %s: %s", i, name, rec.Body.String())
			}
		}
	}
}

// The same, for the errors produced during startup -- the other place a secret
// commonly escapes a process that was careful in its handlers.
func TestTheSecretNeverAppearsInAStartupError(t *testing.T) {
	dir := t.TempDir()

	worldReadable := filepath.Join(dir, "loose-secret")
	if err := os.WriteFile(worldReadable, testSecret, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	empty := filepath.Join(dir, "empty-secret")
	if err := os.WriteFile(empty, []byte("\n  \n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	for name, path := range map[string]string{
		"world readable": worldReadable,
		"empty":          empty,
		"absent":         filepath.Join(dir, "no-such-file"),
		"a directory":    dir,
	} {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig(t)
			cfg.SecretFile = path
			srv, err := cfg.Server(&recordingSink{})
			if err == nil {
				t.Fatalf("a %s secret file was accepted", name)
			}
			if srv != nil {
				t.Error("a refused configuration still produced a server")
			}
			if strings.Contains(err.Error(), string(testSecret)) {
				t.Errorf("the startup error leaks the secret: %v", err)
			}
			// It must name the path, which is how the operator fixes it.
			if !strings.Contains(err.Error(), path) {
				t.Errorf("the error does not name the path %s: %v", path, err)
			}
		})
	}
}

// A world-readable secret is refused rather than warned about.
func TestAWorldReadableSecretIsRefused(t *testing.T) {
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o666, 0o660} {
		t.Run(fmt.Sprintf("mode%04o", mode), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "secret")
			if err := os.WriteFile(path, testSecret, mode); err != nil {
				t.Fatalf("write: %v", err)
			}
			cfg := testConfig(t)
			cfg.SecretFile = path
			if _, err := cfg.Server(&recordingSink{}); err == nil {
				t.Fatalf("mode %04o was accepted", mode)
			}
		})
	}
	// 0600 and 0400 are fine.
	for _, mode := range []os.FileMode{0o600, 0o400} {
		path := filepath.Join(t.TempDir(), "secret")
		if err := os.WriteFile(path, testSecret, mode); err != nil {
			t.Fatalf("write: %v", err)
		}
		cfg := testConfig(t)
		cfg.SecretFile = path
		if _, err := cfg.Server(&recordingSink{}); err != nil {
			t.Errorf("mode %04o was refused: %v", mode, err)
		}
	}
}

// ---------------------------------------------------------- configuration

func TestNoWebhookConfigurationLeavesTheListenerOff(t *testing.T) {
	srv, err := Config{}.Server(&recordingSink{})
	if err != nil {
		t.Fatalf("an unconfigured ingress refused: %v", err)
	}
	if srv != nil {
		t.Fatal("an unconfigured ingress produced a listener")
	}
}

func TestPartialWebhookConfigurationIsAStartupRefusal(t *testing.T) {
	full := testConfig(t)

	partials := map[string]Config{
		"address only":         {Addr: full.Addr},
		"secret only":          {SecretFile: full.SecretFile},
		"installation only":    {InstallationID: testInstallationID},
		"repository id only":   {RepositoryID: testRepositoryID},
		"repository name only": {Repository: testRepository},
		"no address":           {SecretFile: full.SecretFile, InstallationID: testInstallationID, RepositoryID: testRepositoryID, Repository: testRepository},
		"no secret":            {Addr: full.Addr, InstallationID: testInstallationID, RepositoryID: testRepositoryID, Repository: testRepository},
		"no installation":      {Addr: full.Addr, SecretFile: full.SecretFile, RepositoryID: testRepositoryID, Repository: testRepository},
		"no repository id":     {Addr: full.Addr, SecretFile: full.SecretFile, InstallationID: testInstallationID, Repository: testRepository},
		"no repository name":   {Addr: full.Addr, SecretFile: full.SecretFile, InstallationID: testInstallationID, RepositoryID: testRepositoryID},
	}
	for name, cfg := range partials {
		t.Run(name, func(t *testing.T) {
			srv, err := cfg.Server(&recordingSink{})
			if err == nil {
				t.Fatal("a partly configured ingress started")
			}
			if srv != nil {
				t.Error("a refused configuration still produced a server")
			}
		})
	}
}

func TestAConfiguredListenerIsLoopbackOnly(t *testing.T) {
	refused := []string{
		"0.0.0.0:10124",
		":10124",
		"10.0.0.63:10124",
		"96.20.133.54:10124",
		"[::]:10124",
		"example.com:10124",
		"10124",
	}
	for _, addr := range refused {
		t.Run(addr, func(t *testing.T) {
			cfg := testConfig(t)
			cfg.Addr = addr
			srv, err := cfg.Server(&recordingSink{})
			if err == nil {
				t.Fatalf("the ingress accepted %q, which is not loopback", addr)
			}
			if srv != nil {
				t.Error("a refused address still produced a server")
			}
		})
	}
	for _, addr := range []string{"127.0.0.1:10124", "127.0.0.1:0", "[::1]:10124"} {
		cfg := testConfig(t)
		cfg.Addr = addr
		if _, err := cfg.Server(&recordingSink{}); err != nil {
			t.Errorf("the ingress refused loopback %q: %v", addr, err)
		}
	}
}

// Bound for real, on loopback, with the OS choosing the port.
func TestTheListenerBindsLoopbackAndAnswersOnItsOwnEndpoint(t *testing.T) {
	srv, sink := newTestServer(t)
	if err := srv.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer srv.Close()
	go func() { _ = srv.Serve() }()

	host, _, err := splitHostPort(srv.Addr())
	if err != nil {
		t.Fatalf("addr %q: %v", srv.Addr(), err)
	}
	if !isLoopback(host) {
		t.Fatalf("bound %s, which is not loopback", srv.Addr())
	}

	payload := defaultComment().bytes(t)
	req, err := http.NewRequest(http.MethodPost, "http://"+srv.Addr()+Endpoint, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set(SignatureHeader, sign(testSecret, payload))
	req.Header.Set(EventHeader, "issue_comment")
	req.Header.Set(DeliveryHeader, "d-live")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if n := len(sink.deliveries()); n != 1 {
		t.Errorf("%d sink deliveries, want 1", n)
	}

	// A path this surface does not serve is a 404, not a differently
	// authenticated route.
	other, err := http.Post("http://"+srv.Addr()+"/mcp", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("post /mcp: %v", err)
	}
	defer other.Body.Close()
	if other.StatusCode != http.StatusNotFound {
		t.Errorf("the webhook listener answered /mcp with %d; the two surfaces are not separate", other.StatusCode)
	}
}

// ------------------------------------------------------------------- sink

func TestTheObservationSinkRecordsMetadataAndNeverTheCommentBody(t *testing.T) {
	var out bytes.Buffer
	sink := NewObservationSink(&out)

	secretish := "PLEASE-DO-NOT-LOG-THIS-COMMENT-TEXT"
	err := sink.IssueComment(context.Background(), IssueCommentDelivery{
		DeliveryID:         "12345678-90ab-cdef-1234-567890abcdef",
		Action:             "created",
		InstallationID:     testInstallationID,
		RepositoryID:       testRepositoryID,
		RepositoryFullName: testRepository,
		IssueNumber:        testReviewIssue,
		CommentID:          2200330044,
		CommentBody:        secretish,
		SenderID:           chatGPTUserID,
		SenderLogin:        chatGPTLogin,
		SenderType:         "User",
	})
	if err != nil {
		t.Fatalf("sink: %v", err)
	}

	line := out.String()
	if strings.Contains(line, secretish) {
		t.Errorf("the observation logs the comment body: %s", line)
	}
	for _, want := range []string{
		"github webhook accepted",
		"delivery=12345678-90ab-cdef-1234-567890abcdef",
		"event=issue_comment",
		"repo=" + testRepository,
		"issue=156",
		"comment=2200330044",
		"sender=" + chatGPTLogin,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the observation is missing %q: %s", want, line)
		}
	}
}

// A sink failure is a 500 and not an acceptance: the delivery was authentic but
// was not received.
func TestASinkFailureIsReportedWithoutEchoingItsError(t *testing.T) {
	sink := &recordingSink{fail: fmt.Errorf("the internal detail nobody outside should read")}
	srv, err := testConfig(t).Server(sink)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	rec := signedPost(t, srv, "issue_comment", "d-1", defaultComment().bytes(t))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
	if strings.Contains(body(rec), "internal detail") {
		t.Errorf("the response echoes the sink's error: %s", body(rec))
	}
}

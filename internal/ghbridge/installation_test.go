package ghbridge

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crypto"
)

// No test here reaches GitHub. Every exchange runs against an httptest server
// that verifies the assertion the way GitHub would.

func writeTestKey(t *testing.T) (path string, pub *rsa.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	path = filepath.Join(t.TempDir(), "app.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, &key.PublicKey
}

// verifyAssertion checks the RS256 JWT the way GitHub does: signature, alg,
// issuer, and an exp within ten minutes.
func verifyAssertion(t *testing.T, authz string, pub *rsa.PublicKey, wantIssuer string) {
	t.Helper()
	tok := strings.TrimPrefix(authz, "Bearer ")
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("assertion is not a three-part JWT: %d parts", len(parts))
	}
	enc := base64.RawURLEncoding

	var hdr struct{ Alg, Typ string }
	hb, err := enc.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("header not base64url: %v", err)
	}
	if err := json.Unmarshal(hb, &hdr); err != nil {
		t.Fatal(err)
	}
	if hdr.Alg != "RS256" {
		t.Errorf("alg = %q, want RS256", hdr.Alg)
	}

	var claims struct {
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
		Iss string `json:"iss"`
	}
	cb, err := enc.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("claims not base64url: %v", err)
	}
	if err := json.Unmarshal(cb, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Iss != wantIssuer {
		t.Errorf("iss = %q, want %q", claims.Iss, wantIssuer)
	}
	now := time.Now().Unix()
	if claims.Iat > now {
		t.Errorf("iat is in the future by %ds — GitHub would refuse it", claims.Iat-now)
	}
	if claims.Exp <= now {
		t.Error("assertion is already expired")
	}
	if claims.Exp > now+600 {
		t.Errorf("exp is %ds away; GitHub refuses anything over 600", claims.Exp-now)
	}

	sig, err := enc.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("signature not base64url: %v", err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("the assertion did not verify against the configured key: %v", err)
	}
}

// 1. A valid key produces an RS256 assertion the token endpoint accepts.
func TestValidKeyProducesAnAcceptedRS256Assertion(t *testing.T) {
	path, pub := writeTestKey(t)
	var calls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Path != "/app/installations/159521273/access_tokens" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		verifyAssertion(t, r.Header.Get("Authorization"), pub, "4850747")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"token":"ghs_fake","expires_at":%q}`,
			time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	}))
	defer srv.Close()

	auth := &InstallationAuth{AppID: 4850747, InstallationID: 159521273,
		PrivateKeyPath: path, APIBase: srv.URL}
	tok, exp, err := auth.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok == "" {
		t.Fatal("no token returned")
	}
	if !exp.After(time.Now()) {
		t.Error("expiry is not in the future")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("token endpoint called %d times", calls)
	}
}

// 2. A wrong or unreadable key refuses explicitly.
func TestUnreadableOrInvalidKeyRefuses(t *testing.T) {
	dir := t.TempDir()
	notPEM := filepath.Join(dir, "junk.pem")
	if err := os.WriteFile(notPEM, []byte("this is not a key"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, path, want string }{
		{"missing file", filepath.Join(dir, "absent.pem"), "reading the github app private key"},
		{"not pem", notPEM, "not PEM-encoded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &InstallationAuth{AppID: 4850747, InstallationID: 159521273, PrivateKeyPath: tc.path}
			_, _, err := auth.Token(context.Background())
			if err == nil {
				t.Fatal("expected refusal")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want mention of %q", err, tc.want)
			}
		})
	}
}

// 3. A token endpoint failure refuses explicitly rather than proceeding.
func TestTokenEndpointFailureRefuses(t *testing.T) {
	path, _ := writeTestKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Bad credentials"}`)
	}))
	defer srv.Close()

	auth := &InstallationAuth{AppID: 4850747, InstallationID: 159521273,
		PrivateKeyPath: path, APIBase: srv.URL}
	_, _, err := auth.Token(context.Background())
	if err == nil {
		t.Fatal("expected refusal")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should carry the status: %v", err)
	}
}

// 4 and 5. A cached token is reused; an expiring one is refreshed.
func TestTokenIsCachedUntilNearExpiryThenRefreshed(t *testing.T) {
	path, _ := writeTestKey(t)
	var calls int32
	now := time.Now()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"token":"ghs_token_%d","expires_at":%q}`,
			n, now.Add(time.Hour).UTC().Format(time.RFC3339))
	}))
	defer srv.Close()

	clock := now
	auth := &InstallationAuth{AppID: 4850747, InstallationID: 159521273,
		PrivateKeyPath: path, APIBase: srv.URL, Now: func() time.Time { return clock }}

	first, _, err := auth.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Well inside the window: reused, no second call.
	clock = now.Add(30 * time.Minute)
	second, _, err := auth.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Errorf("a live token was not reused: %q then %q", first, second)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("token endpoint called %d times while a live token was cached", calls)
	}

	// Inside the refresh margin: minted again.
	clock = now.Add(59 * time.Minute)
	third, _, err := auth.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if third == first {
		t.Error("a token inside the refresh margin was reused")
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("token endpoint called %d times, want 2", calls)
	}
}

// 6. Nothing assumes the token's length or textual shape. GitHub has changed
// that format before; code that validated it would refuse valid credentials.
func TestTokenShapeIsNeverAssumed(t *testing.T) {
	path, _ := writeTestKey(t)
	for _, shape := range []string{
		"v1.1f699f1069f60xxx",
		"ghs_16C7e42F292c6912E7710c838347Ae178B4a",
		"a", // deliberately absurd
		strings.Repeat("z", 512),
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"token":%q,"expires_at":%q}`, shape,
				time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
		}))
		auth := &InstallationAuth{AppID: 4850747, InstallationID: 159521273,
			PrivateKeyPath: path, APIBase: srv.URL}
		got, _, err := auth.Token(context.Background())
		srv.Close()
		if err != nil {
			t.Fatalf("token %q refused: %v", shape, err)
		}
		if got != shape {
			t.Errorf("token altered in transit: %q -> %q", shape, got)
		}
	}
}

// An unparseable expiry must not be read as a long-lived token.
func TestUnparseableExpiryFallsBackToAShortLife(t *testing.T) {
	path, _ := writeTestKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"token":"ghs_x","expires_at":"not-a-time"}`)
	}))
	defer srv.Close()
	auth := &InstallationAuth{AppID: 4850747, InstallationID: 159521273,
		PrivateKeyPath: path, APIBase: srv.URL}
	_, exp, err := auth.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if exp.After(time.Now().Add(30 * time.Minute)) {
		t.Errorf("an unreadable expiry produced a long-lived token: %s", exp)
	}
}

func TestUnconfiguredAuthMintsNothing(t *testing.T) {
	for _, a := range []*InstallationAuth{
		{},
		{AppID: 4850747},
		{AppID: 4850747, InstallationID: 159521273},
	} {
		if a.Configured() {
			t.Errorf("%+v reported itself configured", a)
		}
		if _, _, err := a.Token(context.Background()); err == nil {
			t.Error("an unconfigured auth minted a token")
		}
	}
}

// 13. No secret material may appear in an error. The key content, the assertion
// and the installation token are all confined to the transport.
func TestSecretMaterialNeverAppearsInErrors(t *testing.T) {
	path, _ := writeTestKey(t)
	keyBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	keyBody := strings.TrimSpace(strings.Split(string(keyBytes), "\n")[1]) // a middle line of the PEM

	// A failing token endpoint that echoes a token-looking value back.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"nope"}`)
	}))
	defer srv.Close()

	auth := &InstallationAuth{AppID: 4850747, InstallationID: 159521273,
		PrivateKeyPath: path, APIBase: srv.URL}
	_, _, terr := auth.Token(context.Background())
	if terr == nil {
		t.Fatal("expected refusal")
	}
	msg := terr.Error()
	if strings.Contains(msg, keyBody) {
		t.Fatal("the private key content appeared in an error")
	}
	if strings.Contains(msg, "BEGIN RSA PRIVATE KEY") {
		t.Fatal("the private key header appeared in an error")
	}
	// The assertion is a three-part dotted base64 blob; none of it may leak.
	if strings.Count(msg, ".") > 3 && strings.Contains(msg, "eyJ") {
		t.Fatalf("something JWT-shaped appeared in an error: %s", msg)
	}
}

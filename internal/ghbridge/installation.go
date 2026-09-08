package ghbridge

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GitHub App installation authentication for Sensei Code's own GitHub
// operations.
//
// This authenticates the MACHINE side. It says nothing about the reviewer: the
// remote ChatGPT connection still posts through a human GitHub account, so
// running the mailbox as an App does not make that review independent. Five
// facts stay separate and none may be read off another:
//
//	app identity      globulario-sensei-code[bot]   who Sensei Code is to GitHub
//	reviewer          user id 1697116               who answered
//	provider          chatgpt                       which reviewer was assigned
//	transport         github                        how the turn travelled
//	session mode      roles.Unverified              what may be concluded from it
//
// Secret handling: this file reads a private key from a PATH and never returns,
// logs, or embeds its content. The JWT and the installation token are likewise
// confined here — they leave only as an Authorization header built inside this
// package. Errors are constructed from the path and the HTTP status, never from
// the material, because an error string is the most common way a secret escapes
// a process that was careful everywhere else.

// jwtLifetime is how long a minted App JWT claims to be valid.
//
// GitHub refuses anything over ten minutes. Nine leaves room for the response
// to arrive without the token expiring in flight.
const jwtLifetime = 9 * time.Minute

// clockSkew backdates iat.
//
// GitHub rejects a JWT whose iat is in the future by its clock. A machine a few
// seconds fast would otherwise fail authentication intermittently, in a way
// that reads as a credential problem rather than a clock one.
const clockSkew = 60 * time.Second

// tokenRefreshMargin is how long before stated expiry a cached installation
// token stops being used.
//
// Installation tokens last about an hour, but the lifetime is GitHub's to state
// and this package reads it from the response rather than assuming it. The
// margin exists so a token cannot expire between the check and the call.
const tokenRefreshMargin = 2 * time.Minute

// InstallationAuth mints installation access tokens for one App installation.
//
// The zero value is unusable: an auth with no app id, installation id or key
// path authenticates nothing rather than falling back to anything.
type InstallationAuth struct {
	AppID          int64
	InstallationID int64
	PrivateKeyPath string
	// APIBase is the GitHub REST root. Empty means api.github.com; tests point
	// it at a local server so no unit test ever reaches GitHub.
	APIBase string
	// HTTP is the client used. Empty means http.DefaultClient.
	HTTP *http.Client
	// Now is the clock, overridable so expiry behaviour is testable without
	// sleeping through it.
	Now func() time.Time

	mu        sync.Mutex
	cached    string
	expiresAt time.Time
	// granted is what GitHub said this installation may do, captured from the
	// token response. Guarded by mu with the token it arrived with, because a
	// refreshed token can carry different grants: an owner may widen or narrow
	// an installation at any time, and a permission set cached past its token
	// would answer for a grant that no longer exists.
	granted map[string]string
}

// Configured reports whether this auth can mint anything.
func (a *InstallationAuth) Configured() bool {
	return a != nil && a.AppID != 0 && a.InstallationID != 0 && strings.TrimSpace(a.PrivateKeyPath) != ""
}

func (a *InstallationAuth) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *InstallationAuth) apiBase() string {
	if b := strings.TrimSpace(a.APIBase); b != "" {
		return strings.TrimSuffix(b, "/")
	}
	return "https://api.github.com"
}

func (a *InstallationAuth) httpClient() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// token returns a live installation access token and its expiry.
//
// Deliberately UNEXPORTED. The law is
//
//	private key -> JWT -> installation token -> Authorization header
//
// and an exported accessor would add "-> any caller who imports this package",
// which is a different and much weaker law. AppClient consumes this internally;
// nothing outside ghbridge can obtain the credential. A live proof that needs to
// demonstrate App authentication does so through an AppClient operation — a
// repository read or a comment post — rather than by exporting the secret.
//
// A cached token is reused until tokenRefreshMargin before the expiry GitHub
// stated. Nothing here inspects the token's length or shape: GitHub has changed
// that format before, and code that validated it would have started refusing
// valid credentials.
func (a *InstallationAuth) token(ctx context.Context) (string, time.Time, error) {
	if !a.Configured() {
		return "", time.Time{}, errors.New("github app authentication is not configured (app id, installation id and private key path are all required)")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cached != "" && a.now().Add(tokenRefreshMargin).Before(a.expiresAt) {
		return a.cached, a.expiresAt, nil
	}

	assertion, err := a.mintJWT()
	if err != nil {
		return "", time.Time{}, err
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", a.apiBase(), a.InstallationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Authorization", "Bearer "+assertion)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := a.httpClient().Do(req)
	if err != nil {
		// The URL carries the installation id, which is not secret. The
		// assertion travelled in a header and is not part of this error.
		return "", time.Time{}, fmt.Errorf("requesting an installation token: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Status and GitHub's message only. The response body of a FAILED token
		// request carries no credential, but a successful one does, which is
		// why this branch is the only one that quotes a body at all.
		return "", time.Time{}, fmt.Errorf("installation token request refused: HTTP %d: %s",
			resp.StatusCode, oneLineLimited(string(body), 200))
	}

	var out struct {
		Token       string            `json:"token"`
		ExpiresAt   string            `json:"expires_at"`
		Permissions map[string]string `json:"permissions"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", time.Time{}, fmt.Errorf("installation token response could not be read: %w", err)
	}
	if strings.TrimSpace(out.Token) == "" {
		return "", time.Time{}, errors.New("installation token response carried no token")
	}

	// GitHub states what this installation may do in the same response that
	// carries the token, and this package used to discard it. Keeping it is what
	// lets a missing grant be reported as a missing grant. A permission map is
	// not credential material -- it names capabilities, never secrets -- so it
	// may appear in an error where the token beside it never can.
	a.granted = out.Permissions

	// Expiry is GitHub's statement, not this package's assumption. If it cannot
	// be parsed, fall back to a conservative short life rather than treating the
	// token as long-lived.
	exp, perr := time.Parse(time.RFC3339, strings.TrimSpace(out.ExpiresAt))
	if perr != nil || exp.IsZero() {
		exp = a.now().Add(10 * time.Minute)
	}

	a.cached = out.Token
	a.expiresAt = exp
	return a.cached, a.expiresAt, nil
}

// mintJWT builds the RS256 App assertion.
//
// Written out rather than pulled from a JWT library: the claim set is three
// fields, and a dependency here would be one more package with access to the
// signing key.
func (a *InstallationAuth) mintJWT() (string, error) {
	key, err := a.loadKey()
	if err != nil {
		return "", err
	}
	now := a.now()

	header := `{"alg":"RS256","typ":"JWT"}`
	claims := fmt.Sprintf(`{"iat":%d,"exp":%d,"iss":"%s"}`,
		now.Add(-clockSkew).Unix(),
		now.Add(jwtLifetime).Unix(),
		strconv.FormatInt(a.AppID, 10))

	enc := base64.RawURLEncoding
	signing := enc.EncodeToString([]byte(header)) + "." + enc.EncodeToString([]byte(claims))

	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("signing the app assertion: %w", err)
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

// loadKey reads the signing key from disk.
//
// The key is read on each mint rather than held in memory for the process
// lifetime: a rotated key takes effect without a restart, and the material
// spends less time resident. Errors name the PATH and never the content.
func (a *InstallationAuth) loadKey() (*rsa.PrivateKey, error) {
	path := strings.TrimSpace(a.PrivateKeyPath)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the github app private key at %s: %w", path, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("the file at %s is not PEM-encoded", path)
	}
	// GitHub issues PKCS#1; PKCS#8 is accepted so a re-encoded key still works.
	if k, perr := x509.ParsePKCS1PrivateKey(block.Bytes); perr == nil {
		return k, nil
	}
	parsed, perr := x509.ParsePKCS8PrivateKey(block.Bytes)
	if perr != nil {
		return nil, fmt.Errorf("the key at %s is not an RSA private key this package can read", path)
	}
	k, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("the key at %s is not RSA", path)
	}
	return k, nil
}

func oneLineLimited(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " "))
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// ErrMissingPermission reports a capability this installation was never granted.
var ErrMissingPermission = errors.New("the github app installation lacks a required permission")

// GrantedPermissions reports what GitHub says this installation may do.
//
// It mints or reuses a token to learn this, because the grants arrive with the
// token rather than from a separate endpoint. A copy is returned so a caller
// cannot mutate what the next check will read.
func (a *InstallationAuth) GrantedPermissions(ctx context.Context) (map[string]string, error) {
	if _, _, err := a.token(ctx); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]string, len(a.granted))
	for k, v := range a.granted {
		out[k] = v
	}
	return out, nil
}

// RequireWrite establishes that this installation holds write on a capability.
//
// ABSENT and READ are reported differently, because they send an operator to
// different places: a capability that was never added must be added to the App
// and the installation update accepted, while one granted read-only is a
// narrower change. Reporting either as a bare 403 sends them to neither.
func (a *InstallationAuth) RequireWrite(ctx context.Context, capability string) error {
	perms, err := a.GrantedPermissions(ctx)
	if err != nil {
		return err
	}
	level, held := perms[capability]
	switch {
	case !held:
		return fmt.Errorf("%w: %q is not granted to installation %d (granted: %s); add it to the "+
			"GitHub App under Permissions & events and accept the installation update",
			ErrMissingPermission, capability, a.InstallationID, describePermissions(perms))
	case level != "write":
		return fmt.Errorf("%w: %q is granted %q rather than \"write\" to installation %d; "+
			"raise it under Permissions & events and accept the installation update",
			ErrMissingPermission, capability, level, a.InstallationID)
	}
	return nil
}

// describePermissions renders a grant set for an error message. Capability
// names and levels only: this map never carries a secret, and the token it
// arrived beside never reaches here.
func describePermissions(perms map[string]string) string {
	if len(perms) == 0 {
		return "none"
	}
	names := make([]string, 0, len(perms))
	for k, v := range perms {
		names = append(names, k+"="+v)
	}
	sort.Strings(names)
	return strings.Join(names, " ")
}

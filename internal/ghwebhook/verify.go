package ghwebhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

// SignatureHeader carries GitHub's HMAC over the raw request body.
const SignatureHeader = "X-Hub-Signature-256"

// DeliveryHeader is GitHub's unique id for one delivery attempt.
const DeliveryHeader = "X-GitHub-Delivery"

// EventHeader names the event a delivery carries.
const EventHeader = "X-GitHub-Event"

// signaturePrefix is the only algorithm form accepted.
//
// Stated rather than parsed out of the header. A verifier that read the
// algorithm from the message would let the sender choose it, and the sender is
// the party being authenticated. There is no sha1= path here: GitHub still
// sends X-Hub-Signature for compatibility, and accepting it would mean an
// attacker who can forge SHA-1 chooses which header this reads.
const signaturePrefix = "sha256="

// ErrUnauthenticated reports that a request did not prove GitHub sent it.
//
// ONE error for every way that can happen — header absent, wrong form, wrong
// length, wrong MAC. The distinctions are real but they are not the caller's
// business, and reporting which stage failed tells an attacker which half of a
// forgery attempt was already right.
var ErrUnauthenticated = errors.New("the request did not carry a valid GitHub signature")

// verify checks the HMAC-SHA256 over EXACTLY the bytes that were received.
//
// The bytes, not a re-encoding of them. A verifier that unmarshalled and
// re-marshalled would authenticate a document that GitHub never sent, because
// key order, escaping, and unicode normalization are all things a round trip
// may change and a MAC will not forgive.
//
// The comparison is constant-time. A byte-by-byte compare that returns early
// leaks, in its timing, how much of a candidate MAC was correct, and a MAC is
// exactly the kind of value an attacker can submit repeatedly.
//
// Nothing here — not the secret, not the expected MAC, not the received one —
// reaches the returned error.
func verify(secret, body []byte, header string) error {
	header = strings.TrimSpace(header)
	if header == "" {
		return ErrUnauthenticated
	}
	if !strings.HasPrefix(header, signaturePrefix) {
		return ErrUnauthenticated
	}
	// Decoded rather than compared as text: hex is case-insensitive, and two
	// strings that differ only in case are the same MAC. Comparing the text
	// would refuse a valid signature written in upper case.
	received, err := hex.DecodeString(header[len(signaturePrefix):])
	if err != nil {
		return ErrUnauthenticated
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	if !hmac.Equal(received, mac.Sum(nil)) {
		return ErrUnauthenticated
	}
	return nil
}

// sign produces the header value for a body. Test-facing, and deliberately in
// the non-test build so the production verifier and the test signer cannot
// drift into agreeing with each other about something GitHub does not do.
func sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// loadSecret reads the shared secret from a path.
//
// The PATH is configuration; the CONTENT never is. It is not returned to the
// caller's configuration, not stored anywhere but the Server, and not present
// in any error this function produces — every message below is built from the
// path and the file's mode.
//
// A secret readable by group or other is REFUSED rather than warned about. The
// entire security of this ingress is that only GitHub and this process know
// these bytes; a mode that widens "this process" to "anyone with a login" has
// already ended that, and starting anyway would serve a surface whose
// authentication is decorative.
func loadSecret(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("the github webhook needs a secret file path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading the github webhook secret at %s: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("the github webhook secret path %s is a directory", path)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return nil, fmt.Errorf(
			"the github webhook secret at %s is mode %04o and readable beyond its owner; "+
				"chmod 600 it rather than serving a surface whose authentication anyone on this machine can forge",
			path, mode)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the github webhook secret at %s: %w", path, err)
	}
	// A trailing newline is what every editor and every `> file` adds, and
	// GitHub signs with the secret the operator pasted into the form. Trimming
	// surrounding whitespace makes the file agree with the form; nothing inside
	// the secret is touched.
	secret := []byte(strings.TrimSpace(string(raw)))
	if len(secret) == 0 {
		return nil, fmt.Errorf("the github webhook secret at %s is empty", path)
	}
	return secret, nil
}

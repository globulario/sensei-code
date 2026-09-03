// Package control is the remote control surface: one running Sensei Code,
// reachable over loopback by a capable agent that has been given its token.
//
// Three things prove three different things here, and none of them may stand in
// for another:
//
//	bearer credential   this connection may reach this control surface
//	principal lease     which role this connection currently holds
//	independence proof  a review context was isolated
//
// The first is this file. It is the weakest of the three and it is the one most
// likely to be mistaken for the others, because it is the one that makes a call
// succeed. Authenticating grants no role: a connection that presents a valid
// token and holds no lease may read the tool list and nothing else.
//
// The identity a decision is attributed to is derived FROM the credential and
// never taken from the request. That is not defensive habit; it is the same law
// internal/principal enforces one layer up. A client that could state its own
// principal could register twice under two names and manufacture the appearance
// of two parties — which is exactly what qualify.go refuses to read as
// independence, and what this file refuses to let it obtain in the first place.
package control

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/globulario/sensei-code/internal/principal"
)

// Credential is a bearer token this server minted, and the principal identity
// bound to it.
//
// The token is unexported and there is exactly one accessor, used once to show
// an operator what to configure. Everything else about this type — String,
// MarshalJSON — redacts, so a credential that finds its way into an event
// payload or a diagnostic renders as a description rather than as the secret.
// A token in the session JSONL would be a durable copy of a live credential in
// a file the whole point of which is to be read later.
type Credential struct {
	token       string
	principalID principal.ID
}

// tokenBytes is the entropy of a minted token. 32 bytes is not a considered
// trade-off; it is simply past the point where the size is the interesting
// question.
const tokenBytes = 32

// Mint generates a new credential.
func Mint() (Credential, error) {
	var b [tokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return Credential{}, err
	}
	return credentialFor(hex.EncodeToString(b[:])), nil
}

// TokenLength is the exact representation Mint produces: 64 lowercase hex
// characters over 32 random bytes.
const TokenLength = 2 * tokenBytes

// FromToken rebuilds a credential from a token an operator already holds, so a
// restarted server can keep the same identity rather than inventing a second
// principal for the same party.
//
// It requires the same strength Mint produces, and that is not pedantry about
// format. The bearer boundary is worth exactly what the weakest token that can
// pass through it is worth, and an operator-supplied secret is the path where a
// three-character value arrives -- SENSEI_CODE_CONTROL_TOKEN=abc, set once
// while trying something out, silently turning a 256-bit boundary into a
// guessable one. A credential that came from the environment is not a weaker
// kind of credential; it is the same kind, obtained differently.
func FromToken(token string) (Credential, error) {
	t := strings.TrimSpace(token)
	if t == "" {
		return Credential{}, errors.New("a control credential cannot be empty")
	}
	if len(t) != TokenLength {
		return Credential{}, fmt.Errorf(
			"a control credential must be %d hex characters, the same strength this server mints; got %d",
			TokenLength, len(t))
	}
	if t != strings.ToLower(t) {
		return Credential{}, errors.New("a control credential must be lowercase hex, so one secret has one spelling and one principal")
	}
	if _, err := hex.DecodeString(t); err != nil {
		return Credential{}, fmt.Errorf("a control credential must be hex, the same representation this server mints: %w", err)
	}
	return credentialFor(t), nil
}

func credentialFor(token string) Credential {
	return Credential{token: token, principalID: principalFor(token)}
}

// principalFor derives the authority-bearing identity from the credential.
//
// Deterministic, so reconnecting with the same token is the same party and a
// client cannot obtain a second authority identity by saying another name. One
// way, so the identity may be recorded, logged and compared without being a
// copy of the secret. Domain-separated, so the value cannot collide with any
// other hash this project takes of the same bytes.
//
// One token is one principal. Two genuinely distinct remote parties therefore
// need two credentials, which is the honest shape: telling two parties apart is
// a property of what they were issued, not of what they call themselves.
func principalFor(token string) principal.ID {
	sum := sha256.Sum256([]byte("sensei-code.control.principal\x00" + token))
	return principal.ID("remote:" + hex.EncodeToString(sum[:])[:16])
}

// Principal is the identity every decision made under this credential is
// attributed to.
func (c Credential) Principal() principal.ID { return c.principalID }

// Configured reports whether this credential can authenticate anything.
func (c Credential) Configured() bool { return strings.TrimSpace(c.token) != "" }

// Token is the secret, and the only place it leaves this type.
//
// Named plainly rather than hidden behind something softer, so that every call
// site reads as what it is and shows up in a grep for the one thing that must
// not be logged.
func (c Credential) Token() string { return c.token }

// Authenticates reports whether a presented token is this credential's.
//
// Constant time, and an empty presentation is refused before the comparison
// rather than by it. subtle.ConstantTimeCompare returns 0 for unequal lengths,
// so an empty token would already fail — but relying on that makes the refusal
// of "no credential at all" an accident of the comparison rather than a stated
// rule, and an unconfigured server would then be one length check away from
// authenticating everybody.
func (c Credential) Authenticates(presented string) bool {
	if !c.Configured() || strings.TrimSpace(presented) == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.token), []byte(presented)) == 1
}

// String describes the credential without disclosing it.
func (c Credential) String() string {
	if !c.Configured() {
		return "control credential: none configured"
	}
	return "control credential for " + string(c.principalID) + " (token redacted)"
}

// MarshalJSON redacts.
//
// It exists because the failure it prevents is not a call somebody makes on
// purpose. A Credential reaching an event payload, a structured diagnostic or a
// receipt would be marshalled by reflection over its fields, and unexported
// fields marshal to {} today — which is safe by accident and would stop being
// safe the moment somebody exported one for a good reason. This makes the
// redaction the type's own answer rather than a property of its field
// visibility.
func (c Credential) MarshalJSON() ([]byte, error) {
	return []byte(`{"principal":"` + string(c.principalID) + `","token":"redacted"}`), nil
}

// BearerToken extracts the presented token from an Authorization header value.
// Anything that is not a well-formed Bearer presentation yields "", which
// Authenticates then refuses.
func BearerToken(header string) string {
	const prefix = "bearer "
	h := strings.TrimSpace(header)
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

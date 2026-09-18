package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrUnavailable reports that a provider PROVED it cannot serve a turn right
// now, for a reason that is its own and temporary.
//
// It is established here, at the adapter, and nowhere else. The workflow reads
// the type; it never reads a message. That boundary is the whole point: the
// first dogfood run (2026-09-18) died on "You've hit your usage limit ... try
// again at Sep 19th, 2026 7:10 AM", and the tempting repair is to match that
// sentence. A sentence is presentation. The same words can come from a model's
// own output, a wrapped runner failure, or a test fixture, and a matcher that
// accepts them turns ordinary failures into "come back later" -- the opposite
// of fail closed.
//
// So a runner failure is NOT unavailability unless the provider's structured
// protocol said so. Everything else stays an ordinary error.
var ErrUnavailable = errors.New("the provider is temporarily unavailable")

// Unavailable is one provider's proven, temporary refusal to serve a turn.
//
// It is transport state, not role output: no answer was produced, so there is
// nothing to call malformed and nothing to review.
type Unavailable struct {
	// Provider is the configured provider id that refused.
	Provider string
	// Reason is the provider's own structured code, verbatim -- for the Codex
	// app-server, the codexErrorInfo value. Never a paraphrase.
	Reason string
	// Detail is the provider's human-readable message. For people only: no
	// decision may be taken from it, and RetryAt is never parsed out of it.
	Detail string
	// RetryAt is when the provider said it will serve again. Zero means the
	// provider supplied no reliable structured time, which is recorded as
	// UNKNOWN -- never inferred from Detail or from configuration.
	RetryAt time.Time
}

func (u *Unavailable) Error() string {
	retry := "retry time UNKNOWN"
	if !u.RetryAt.IsZero() {
		retry = "retry at " + u.RetryAt.UTC().Format(time.RFC3339)
	}
	msg := fmt.Sprintf("%v: %s reported %s (%s)", ErrUnavailable, u.Provider, u.Reason, retry)
	if d := strings.TrimSpace(u.Detail); d != "" {
		msg += ": " + d
	}
	return msg
}

func (u *Unavailable) Unwrap() error { return ErrUnavailable }

// AsUnavailable finds a proven provider unavailability anywhere in err's chain.
//
// It matches the TYPE, not ErrUnavailable: a bare sentinel wrapped by some
// other layer carries no provider and no reason, and is not proof of anything.
func AsUnavailable(err error) (*Unavailable, bool) {
	var u *Unavailable
	if errors.As(err, &u) && u != nil {
		return u, true
	}
	return nil, false
}

// codexUnavailableReasons is the closed set of Codex app-server codexErrorInfo
// values that prove temporary unavailability. Read by MEMBERSHIP: a code nobody
// listed -- "other", "internalServerError", "badRequest", a variant a later
// Codex adds -- stays an ordinary failure until someone establishes otherwise.
//
// usageLimitExceeded is the one measured on the live wire (codex-cli 0.150.1,
// 2026-09-18): turn/completed status "failed", error.codexErrorInfo
// "usageLimitExceeded", willRetry false. That turn's error carried no reset
// time, and the rate-limit update sent during it named a different limit with
// every field null, so the adapter reports the retry time as UNKNOWN rather than
// borrowing one from a separate account read.
var codexUnavailableReasons = map[string]bool{
	"usageLimitExceeded": true,
}

// codexTurnUnavailable classifies a failed Codex app-server turn from its
// structured error info. It returns nil unless the info is a bare string code in
// codexUnavailableReasons; an object-shaped variant carries an HTTP status, not
// a statement of temporary unavailability.
func codexTurnUnavailable(providerID string, info json.RawMessage, message string) *Unavailable {
	var code string
	if len(info) == 0 || json.Unmarshal(info, &code) != nil {
		return nil
	}
	if !codexUnavailableReasons[code] {
		return nil
	}
	return &Unavailable{Provider: providerID, Reason: code, Detail: strings.TrimSpace(message)}
}

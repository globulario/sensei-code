package workflow

// G1, the read-side identity handshake.
//
// Law 2: a valid response from the wrong Oxigraph instance is still the wrong
// graph and must refuse. Law 3: production readers resolve identity through one
// owner and must not independently choose a port.
//
// Today sensei-code takes the domain it governs by from `binding.repository_domain`
// in the awareness service's OWN answer (internal/sensei RepositoryDomain). Nothing
// checks that answer against the repository on disk, so a consumer pointed at a
// healthy service for another repository governs by that service's domain and
// believes it.
//
// Measured on this machine: three Oxigraph stores answer healthily and hold
// different graphs — :7878 with 237,049 triples served by nothing and declared as
// netcfg's default, :7881 with 142,739 for the sensei domain, :7882 with 35,268 for
// sensei-code. `internal/ghbridge/transport.go` has 1 triple in :7882 and 0 in the
// other two, so a wrong-instance read returns confident answers about the wrong
// repository rather than failing.

import (
	"errors"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/sensei"
)

func TestTheDomainIsDerivedFromTheRepositoryNotTheService(t *testing.T) {
	for name, tc := range map[string]struct {
		remote, want string
	}{
		"ssh":             {"git@github.com:globulario/sensei-code.git", "github.com/globulario/sensei-code"},
		"https with .git": {"https://github.com/globulario/sensei-code.git", "github.com/globulario/sensei-code"},
		"https bare":      {"https://github.com/globulario/sensei-code", "github.com/globulario/sensei-code"},
		"trailing slash":  {"https://github.com/globulario/sensei-code/", "github.com/globulario/sensei-code"},
		"ssh scheme":      {"ssh://git@github.com/globulario/sensei-code.git", "github.com/globulario/sensei-code"},
		"other host":      {"git@gitlab.com:acme/thing.git", "gitlab.com/acme/thing"},
	} {
		if got := domainFromRemote(tc.remote); got != tc.want {
			t.Errorf("%s: domainFromRemote(%q) = %q, want %q", name, tc.remote, got, tc.want)
		}
	}
	// A remote it cannot read yields "", never a guess. An invented identity
	// would be worse than an absent one: it would be compared and would agree.
	for _, unreadable := range []string{"", "   ", "not a url", "https://github.com/onlyowner"} {
		if got := domainFromRemote(unreadable); got != "" {
			t.Errorf("domainFromRemote(%q) invented %q", unreadable, got)
		}
	}
}

// The refusal: a healthy service for another repository must not be governed by.
func TestAGraphClaimingAnotherRepositoryIsRefused(t *testing.T) {
	err := verifyGraphDomain("github.com/globulario/sensei-code", "github.com/globulario/sensei", "localhost:10121")
	var mismatch *graphDomainMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("err = %v, want a typed graphDomainMismatchError", err)
	}
	for _, want := range []string{"github.com/globulario/sensei-code", "github.com/globulario/sensei", "localhost:10121"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal omits %q: %v", want, err)
		}
	}
	// It must say the answer was healthy, or a reader will look for an outage.
	lower := strings.ToLower(err.Error())
	if !strings.Contains(lower, "answered") && !strings.Contains(lower, "healthy") {
		t.Errorf("the refusal does not say the wrong graph answered successfully: %v", err)
	}
}

func TestAMatchingDomainIsAccepted(t *testing.T) {
	if err := verifyGraphDomain("github.com/globulario/sensei-code", "github.com/globulario/sensei-code", "localhost:10122"); err != nil {
		t.Fatalf("a graph claiming this repository was refused: %v", err)
	}
	// Case and trailing whitespace are transport noise, not identity.
	if err := verifyGraphDomain("github.com/globulario/sensei-code", " GitHub.com/globulario/sensei-code ", "a"); err != nil {
		t.Errorf("normalisation rejected an equal identity: %v", err)
	}
}

// Absence must fail closed in the direction that does not manufacture agreement.
func TestUnknownIdentityDoesNotBecomeAgreement(t *testing.T) {
	// The graph claims nothing: it cannot be verified, so it cannot be trusted.
	if err := verifyGraphDomain("github.com/globulario/sensei-code", "", "localhost:10122"); err == nil {
		t.Error("a graph that claims no domain was accepted; an unverifiable identity is not a matching one")
	}
	// The repository identity could not be derived: refuse rather than accept
	// whatever the service says, which is the substitution G1 exists to remove.
	if err := verifyGraphDomain("", "github.com/globulario/sensei", "localhost:10121"); err == nil {
		t.Error("with no repository identity to compare, the service's claim was accepted unchecked")
	}
}

// The gate must perform the handshake, in BOTH lanes.
//
// The degraded path deliberately lets an observation past a graph that is BEHIND,
// because the condition that makes investigation valuable must not forbid it. A
// graph for another repository is a different thing: it is not behind, it is about
// somebody else's code, and it answers confidently. An observation built on it
// would report findings about the wrong repository — worse than reporting none.
func TestGateRefusesAWrongRepositoryGraph(t *testing.T) {
	wrong := sensei.ToolResult{Structured: map[string]any{
		"composition_state": "complete",
		"binding":           map[string]any{"repository_domain": "github.com/globulario/sensei"},
	}}
	ok := sensei.ToolResult{Structured: map[string]any{
		"status": "PREFLIGHT_STATUS_OK", "risk_class": "ARCHITECTURE_SENSITIVE",
		"authority": map[string]any{"authoritative": true,
			"graph_freshness_state": "GRAPH_FRESHNESS_STATE_CURRENT", "seed_state": "SEED_STATE_CURRENT"},
	}}

	for _, observing := range []bool{false, true} {
		_, err := certifyStartForLane(wrong, ok, "head", "github.com/globulario/sensei-code", "localhost:10121", observing)
		if err == nil {
			t.Fatalf("observing=%v: the gate certified a start against another repository's graph", observing)
		}
		var mismatch *graphDomainMismatchError
		if !errors.As(err, &mismatch) {
			t.Errorf("observing=%v: refusal is not the typed identity mismatch: %v", observing, err)
		}
		if !strings.Contains(err.Error(), "localhost:10121") {
			t.Errorf("observing=%v: the refusal does not name the endpoint that answered: %v", observing, err)
		}
	}

	// And the same gate accepts the repository's own graph, so the check
	// discriminates rather than refusing everything.
	right := sensei.ToolResult{Structured: map[string]any{
		"composition_state": "complete",
		"binding":           map[string]any{"repository_domain": "github.com/globulario/sensei-code"},
	}}
	if _, err := certifyStartForLane(right, ok, "head", "github.com/globulario/sensei-code", "localhost:10122", false); err != nil {
		t.Fatalf("the gate refused this repository's own graph: %v", err)
	}
}

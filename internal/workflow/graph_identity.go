package workflow

// The read-side graph identity handshake (slice G1 of the graph-identity front,
// docs/architecture/oxygraph_usage.md in the sensei repository).
//
// Law 2: a valid response from the wrong Oxigraph instance is still the wrong
// graph and must refuse. Law 3: production readers resolve identity through one
// owner and must not independently choose a port.
//
// Before this, the domain a run governed by came from `binding.repository_domain`
// in the awareness service's own answer, and nothing compared it to the repository
// on disk. A consumer pointed at a healthy service for another repository governed
// by that service's domain and believed it -- the answer is well-formed, the
// service is healthy, and the graph is about somebody else's code.
//
// Three stores answer healthily on the development machine and hold different
// graphs (237,049 / 142,739 / 35,268 triples), and the one netcfg declares as the
// production default is served by nothing. So this is not a hypothetical.
//
// The repair is deliberately small: derive the identity INDEPENDENTLY from the
// checkout's own remote, compare, and refuse on disagreement. It adds no store URL
// plumbing and changes no publication behaviour.

import (
	"fmt"
	"regexp"
	"strings"
)

// remoteShape matches the two forms a git remote takes, capturing host and path.
//
//	scp-like   git@github.com:owner/name.git
//	URL-like   https://github.com/owner/name(.git)
var remoteShape = regexp.MustCompile(`^(?:[a-zA-Z][a-zA-Z0-9+.-]*://)?(?:[^@/]+@)?([^/:]+)[:/](.+)$`)

// domainFromRemote derives the repository identity a graph must claim, from the
// checkout's own remote URL.
//
// Returns "" when it cannot be derived. That matters more than the happy path: an
// invented identity would be COMPARED, and a guess that happens to agree is worse
// than no answer, because it would silence the very check this exists to perform.
func domainFromRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	m := remoteShape.FindStringSubmatch(remote)
	if m == nil {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(m[1]))
	path := strings.Trim(strings.TrimSuffix(strings.TrimSpace(m[2]), ".git"), "/")
	if host == "" || path == "" {
		return ""
	}
	// A repository identity is host plus at least owner and name. One segment is
	// not a repository, and accepting it would produce an identity that compares.
	if len(strings.Split(path, "/")) < 2 {
		return ""
	}
	return host + "/" + path
}

// graphDomainMismatchError refuses a graph that is healthy and about the wrong
// repository.
type graphDomainMismatchError struct {
	// Repository is the identity derived from the checkout.
	Repository string
	// Claimed is what the awareness service said its graph is for.
	Claimed string
	// Address is the awareness endpoint that answered.
	Address string
}

func (e *graphDomainMismatchError) Error() string {
	claimed := e.Claimed
	if strings.TrimSpace(claimed) == "" {
		claimed = "(the graph claimed no domain)"
	}
	repo := e.Repository
	if strings.TrimSpace(repo) == "" {
		repo = "(the repository identity could not be derived from its remote)"
	}
	return "refusing to govern this repository with a graph that is not its own.\n" +
		"  this repository  " + repo + "\n" +
		"  the graph at " + e.Address + " answered successfully, for  " + claimed + "\n\n" +
		"Nothing failed and nothing is unreachable: the wrong graph replied, well-formed. " +
		"A healthy answer from another repository's graph would be read as evidence about this one, " +
		"so it is refused rather than used. Point the awareness address at this repository's graph, " +
		"or correct the domain that graph is built for."
}

// verifyGraphDomain is the handshake.
//
// Fails closed in both directions of absence. A graph that claims no domain cannot
// be verified, and an unverifiable identity is not a matching one. A repository
// whose identity could not be derived gives nothing to compare against, and
// accepting the service's claim there would reinstate exactly the substitution this
// check removes -- the reader trusting the thing it is supposed to be checking.
func verifyGraphDomain(repository, claimed, address string) error {
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	r, c := norm(repository), norm(claimed)
	if r != "" && c != "" && r == c {
		return nil
	}
	return &graphDomainMismatchError{Repository: repository, Claimed: claimed, Address: address}
}

// awarenessAddress renders the endpoint a binding used, for a refusal a reader can
// act on. It reports the configured argument rather than a resolved socket, because
// the argument is what an operator edits.
func awarenessAddress(args []string) string {
	for i, a := range args {
		if strings.HasPrefix(a, "--awareness-addr=") {
			return strings.TrimPrefix(a, "--awareness-addr=")
		}
		if a == "--awareness-addr" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return fmt.Sprintf("the configured awareness command %v", args)
}

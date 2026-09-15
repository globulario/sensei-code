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

// --- LAW 5 ACROSS CALLS ------------------------------------------------------
//
// The handshake above answers "is this graph about my repository?" once, at the
// start. Law 5 asks a second question, repeatedly: is the graph answering NOW the
// one this run pinned?
//
// A single audit cannot check that for itself. Sensei's evaluator brackets its own
// queries and refuses a switch inside one audit, but nothing there knows which
// generation certified this run's start. Between the certifying preflight and the
// admitting audit a graph can be rebuilt -- three live stores on this machine answer
// healthily with different graphs -- and the run would then accept a verdict
// produced by rules it never certified.

// graphGenerationSwitchedError refuses a verdict produced by a graph other than the
// one the run pinned.
//
// It is deliberately not phrased as an outage. The graph answered, healthily, and
// that is the problem: a well-formed verdict from rules this run never certified is
// worse than no verdict, because it carries the authority of one and the content of
// the other.
type graphGenerationSwitchedError struct {
	// Pinned is the generation that certified this run's start.
	Pinned string
	// Observed is the generation that answered the call being checked.
	Observed string
	// Address is the awareness endpoint that answered.
	Address string
	// Operation names the call whose answer is being refused.
	Operation string
}

func (e *graphGenerationSwitchedError) Error() string {
	op := strings.TrimSpace(e.Operation)
	if op == "" {
		op = "a graph query"
	}
	return "refusing a verdict produced by a graph this run did not certify.\n" +
		"  this run pinned generation  " + e.Pinned + "\n" +
		"  " + op + " was answered by    " + e.Observed + "\n" +
		"  endpoint  " + e.Address + "\n\n" +
		"The graph was replaced while this run was executing. Nothing failed and nothing is " +
		"unreachable: a well-formed verdict came back, from rules this run never certified. " +
		"Accepting it would give the authority of the certified start to the content of a " +
		"different generation, so the run refuses instead. Re-run the task against the current " +
		"graph, which will certify its own start."
}

// verifyPinnedGeneration compares the generation that answered a call against the
// one the run pinned at certified start.
//
// Two absences, treated differently and on purpose:
//
//   - NO PIN: nothing to compare. Inventing one would compare a guess, and refusing
//     a graph for failing a comparison that never happened would be a refusal this
//     check cannot justify. The start gate is what refuses an unidentified graph.
//
//   - NO OBSERVED GENERATION: a deployment fact, not a switch. Upstream, an
//     available audit result CANNOT omit it -- diffaudit.AuditResult.Validate
//     refuses exactly that -- so an absent field means the awareness service
//     predates the check. This is the single fail-open branch in this front; it
//     rests on a property of the other side rather than an assumption about it, and
//     TestAnAuditReportingNoGenerationIsNotTreatedAsASwitch is where the reasoning
//     breaks if that lock is ever relaxed.
func verifyPinnedGeneration(pinned, observed, address string) error {
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	p, o := norm(pinned), norm(observed)
	if p == "" || o == "" || p == o {
		return nil
	}
	return &graphGenerationSwitchedError{
		Pinned:   strings.TrimSpace(pinned),
		Observed: strings.TrimSpace(observed),
		Address:  address,
	}
}

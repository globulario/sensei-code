package workflow

// Mechanical governance gates.
//
// These are the transitions Sensei owns rather than advises on. Everything here
// consumes typed structured verdicts; none of it reads prose, and none of it
// asks a model what a verdict meant. A gate that a model can talk its way past
// is not a gate, and the specific way that failure arrives is a reviewer
// accepting a candidate that Sensei refused — the reviewer is not being
// dishonest, it simply never saw the refusal as anything but a paragraph in
// its prompt.

import (
	"fmt"
	"strings"

	"github.com/globulario/sensei-code/internal/sensei"
)

// certifiedStart is proof that Sensei cleared this task to begin.
//
// It carries no behaviour; its entire purpose is that it cannot be constructed
// outside certifyStart. Any function that starts a worker takes one, so
// "somebody added a code path that skips the preflight gate" stops being a
// review question and becomes a compile error. The previous arrangement — call
// the gate, then remember to check its result before the next call — is exactly
// the kind of discipline that holds until the day someone adds a second entry
// point, which is what Resume was.
type certifiedStart struct {
	workspace sensei.WorkspaceStatus
	preflight sensei.PreflightDecision
	// degraded is what the gate could NOT certify, on a start that was allowed
	// through anyway. Empty on a governed run, which refuses instead.
	//
	// It exists so an observation that ran on an uncertified workspace reports
	// that fact rather than presenting its findings as though the graph had
	// vouched for itself. Allowing the read and dropping the caveat would be
	// the same defect this whole line of work keeps finding.
	degraded []string
}

// Degraded is what the gate could not certify on an observation start.
func (c certifiedStart) Degraded() []string { return append([]string(nil), c.degraded...) }

// Domain is the repository domain Sensei bound this workspace to.
func (c certifiedStart) Domain() string { return c.workspace.Binding.RepositoryDomain }

// RiskClass is Sensei's classification of the work.
func (c certifiedStart) RiskClass() string { return c.preflight.RiskClass }

// RequiredActions are the actions Sensei says this change requires.
func (c certifiedStart) RequiredActions() []string { return c.preflight.RequiredActions }

// GraphSourceCommit is the repository commit the graph's corpus was compiled
// from. It is emphatically not the candidate's base: the two differ whenever
// the graph has not been rebuilt since the last commit, and conflating them
// records a human decision against a commit nobody was working on.
func (c certifiedStart) GraphSourceCommit() string { return c.preflight.Authority.SourceRepoCommit }

// GraphBuildCommit is the graph generation that certified this start.
func (c certifiedStart) GraphBuildCommit() string { return c.preflight.Authority.GraphBuildCommit }

// GraphDigest is the digest of the graph actually served to this start.
//
// It is a different fact from GraphBuildCommit and must never stand in for it:
// one names the generation that produced the rules, the other the bytes that
// answered this run.
//
// It is read from the WORKSPACE contract, which is where Sensei reports it. An
// earlier version read the preflight authority block, where the field is empty
// -- so the first real governed run emitted a receipt saying the start "did not
// carry a live graph digest" while the digest was sitting in the other result
// the gate had already decoded. The receipt was right that it had not been
// given one; it was this accessor that looked in the wrong place.
func (c certifiedStart) GraphDigest() string {
	if a := c.workspace.GraphAuthority; a != nil && strings.TrimSpace(a.LiveStoreGraphDigest) != "" {
		return a.LiveStoreGraphDigest
	}
	// Not a fallback to a different fact: the preflight block carries the same
	// field name, and if only it is populated that is still the served graph.
	return c.preflight.Authority.LiveStoreGraphDigest
}

// SourceRepoCommit is the repository commit that graph was built against.
func (c certifiedStart) SourceRepoCommit() string { return c.preflight.Authority.SourceRepoCommit }

// Invariants are the invariants the preflight put directly in scope.
func (c certifiedStart) Invariants() []sensei.Invariant { return c.preflight.DirectInvariants }

// certifyStart runs the start-of-task gate over the two required Sensei
// surfaces. It fails closed: a malformed, empty, unavailable, stale or
// otherwise non-affirmative answer refuses the task rather than starting it.
//
// This runs before the architect is consulted, and its refusal is not
// overridable by the architect's decision. The ordering is the point. An
// architect handed a stale graph will produce a confident, specific, entirely
// plausible plan built on invariants that no longer hold, and the plan will
// read as excellent work.
func certifyStart(workspaceResult, preflightResult sensei.ToolResult, repositoryHead string) (certifiedStart, error) {
	return certifyStartForLane(workspaceResult, preflightResult, repositoryHead, false)
}

// certifyStartForLane is certifyStart with the observation lane accounted for.
//
// The gate above refuses a start the graph cannot certify, and its reason is
// about CHANGES: an architect handed a stale graph writes a confident plan on
// invariants that no longer hold. None of that is a reason to refuse to READ.
//
// Refusing anyway had a specific cost. An audit could not run in exactly the
// situations where an audit is most wanted -- a workspace that is not fully
// composed, a graph that is behind -- so the system could not investigate its
// own degradation without first repairing it. That is the same trap as needing
// coverage before being allowed to look: the condition that makes investigation
// valuable is the condition that forbids it.
//
// So an observation is allowed past a non-affirmative answer, and the answer
// travels with it. What is NOT allowed past is an unreadable surface: a
// ContractError means Sensei was reached and what came back cannot be read at
// all, and an observation that cannot read the graph's own answer has no basis
// for reporting anything the graph said.
func certifyStartForLane(workspaceResult, preflightResult sensei.ToolResult, repositoryHead string, observing bool) (certifiedStart, error) {
	workspace, err := sensei.DecodeWorkspaceStatus(workspaceResult)
	if err != nil {
		return certifiedStart{}, err
	}
	var degraded []string
	if !workspace.Permits() {
		if !observing {
			return certifiedStart{}, fmt.Errorf("Sensei will not certify this workspace: %s", workspace.Diagnostic())
		}
		degraded = append(degraded, "workspace not certified: "+workspace.Diagnostic())
	}

	preflight, err := sensei.DecodePreflight(preflightResult)
	if err != nil {
		return certifiedStart{}, err
	}
	// PermitsStart, not Permits: no plan exists yet, so no file list can be
	// named, and the file-scoped verdict is deferred to the diff audit.
	if !preflight.PermitsStart() {
		if !observing {
			return certifiedStart{}, fmt.Errorf("Sensei will not certify a start for this task: %s", preflight.Diagnostic())
		}
		degraded = append(degraded, "start not certified: "+preflight.Diagnostic())
	}
	// repositoryHead is accepted but deliberately not compared against the
	// graph's SourceRepoCommit.
	//
	// An earlier version did compare them and was wrong. SourceRepoCommit is the
	// commit identity of the rule snapshot, which on this installation belongs
	// to the services repository: the graph server runs with
	// -home-domain github.com/globulario/services. Comparing it against the
	// governed repository's HEAD compares commits from two different
	// repositories, so it can never match, and it produced a refusal telling the
	// human to rebuild a graph that was already current.
	//
	// The parameter is kept because the question -- were these rules compiled
	// from this code -- is a real one worth asking once a field exists that
	// answers it. Inventing the answer from the wrong field was the error, not
	// asking.
	_ = repositoryHead

	return certifiedStart{workspace: workspace, preflight: preflight, degraded: degraded}, nil
}

// sameCommit compares commit identities that may be abbreviated to different
// lengths. Sensei publishes twelve characters; git reports forty.
func sameCommit(a, b string) bool {
	a, b = strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	if a == "" || b == "" {
		return false
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	return strings.HasPrefix(b, a)
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// acceptance is the outcome of the end-of-candidate gate.
type acceptance struct {
	// Accepted reports whether the candidate may conclude.
	Accepted bool
	// Refusal explains a mechanical refusal, empty when there was none.
	Refusal string
	// Overrode records that a reviewer accepted and Sensei refused anyway. This
	// is kept separate from Refusal because it is the interesting event: it
	// means the reviewer and the audit disagreed about a governed question, and
	// a system that resolves that silently teaches nobody anything.
	Overrode bool
}

// judgeCandidate decides whether a reviewed candidate may be accepted.
//
// Sensei's four audit verdicts differ in who they leave the decision to. pass
// and review both leave room for the reviewer — "review" means precisely that a
// reviewer should look. block does not: it is a refusal, and a reviewer
// accepting over it would install the reviewer as the judge of an architectural
// audit. cannot_verify is not permission either; an audit that could not run is
// an unanswered question, and a candidate accepted on an unanswered question
// acquires a receipt it did not earn.
func judgeCandidate(reviewDecision string, audit sensei.DiffAuditDecision) acceptance {
	if strings.ToLower(strings.TrimSpace(reviewDecision)) != "accept" {
		return acceptance{}
	}
	if audit.ReviewerMayAccept() {
		return acceptance{Accepted: true}
	}
	return acceptance{
		Refusal:  audit.Diagnostic(),
		Overrode: true,
	}
}

// reviseInstruction turns a mechanical refusal into work the implementor can
// actually do, rather than a bare denial it will only guess at.
func reviseInstruction(a acceptance) string {
	return strings.TrimSpace("Sensei refused this candidate and that refusal is not negotiable by review. " +
		"Resolve the following before the candidate can be accepted: " + a.Refusal)
}

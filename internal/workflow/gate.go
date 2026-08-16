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
}

// Domain is the repository domain Sensei bound this workspace to.
func (c certifiedStart) Domain() string { return c.workspace.Binding.RepositoryDomain }

// RiskClass is Sensei's classification of the work.
func (c certifiedStart) RiskClass() string { return c.preflight.RiskClass }

// RequiredActions are the actions Sensei says this change requires.
func (c certifiedStart) RequiredActions() []string { return c.preflight.RequiredActions }

// GraphBuildCommit is the graph generation that certified this start.
func (c certifiedStart) GraphBuildCommit() string { return c.preflight.Authority.GraphBuildCommit }

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
func certifyStart(workspaceResult, preflightResult sensei.ToolResult) (certifiedStart, error) {
	workspace, err := sensei.DecodeWorkspaceStatus(workspaceResult)
	if err != nil {
		return certifiedStart{}, err
	}
	if !workspace.Permits() {
		return certifiedStart{}, fmt.Errorf("Sensei will not certify this workspace: %s", workspace.Diagnostic())
	}

	preflight, err := sensei.DecodePreflight(preflightResult)
	if err != nil {
		return certifiedStart{}, err
	}
	// PermitsStart, not Permits: no plan exists yet, so no file list can be
	// named, and the file-scoped verdict is deferred to the diff audit.
	if !preflight.PermitsStart() {
		return certifiedStart{}, fmt.Errorf("Sensei will not certify a start for this task: %s", preflight.Diagnostic())
	}
	return certifiedStart{workspace: workspace, preflight: preflight}, nil
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

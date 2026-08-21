package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/taskstate"
)

// This file holds the adversarial half of the governed loop: what identifies a
// candidate revision, what each role is given, and the receipt that explains why
// the loop branched when two agents disagreed.
//
// It is separate from engine.go because it answers a different question.
// engine.go drives a task from a request to a candidate; this decides what the
// roles judging that candidate are allowed to see and be bound by.
//
// The counterexample hunter is deliberately absent. Its verdict is a new class
// of claim, and until an admitted candidate can carry verdicts into Sensei
// (sensei-code#22) that claim would terminate here — which is agents agreeing
// amongst themselves, the thing the adversarial model exists to prevent.

// candidateRevision identifies the exact bytes a role was shown.
//
// It is computed here rather than taken from Sensei's audit because the two are
// answers to different questions. Sensei's digest identifies what Sensei
// audited, and it is absent whenever the audit could not run — which is exactly
// the run where a stale review would go unnoticed. This one always exists,
// because the diff always exists.
func candidateRevision(diff string) string {
	sum := sha256.Sum256([]byte(diff))
	return hex.EncodeToString(sum[:])[:16]
}

// reportRevision is the identity of one inspection's findings.
//
// A read-only run has no diff to digest, and a review still has to be bound to
// the exact bytes it read: the worker revises its findings between cycles just
// as it revises a change, and a verdict that outlived the text it judged would
// be attached to findings nobody reviewed.
func reportRevision(report string) string {
	sum := sha256.Sum256([]byte(report))
	return hex.EncodeToString(sum[:])[:16]
}

// reviewPacket assembles what an independent reviewer is given.
//
// It carries the governing facts, the artifact, and the evidence produced by
// executing checks. It does not carry the worker's account of its own work, and
// there is nowhere in the type to put it.
func reviewPacket(tc taskContext, binding roles.Binding, start certifiedStart, plan, diff, audit, evidence string) roles.IndependentReviewPacket {
	return roles.IndependentReviewPacket{
		Provenance: roles.Provenance{
			TaskID: binding.TaskID, Role: roles.Reviewer,
			BaseSHA: binding.BaseSHA, CandidateDigest: binding.CandidateDigest,
			GraphBuildCommit: start.GraphBuildCommit(),
			SessionMode:      roles.Fresh,
			At:               time.Now().UTC(),
		},
		Task:               tc.Task,
		Plan:               plan,
		Conversation:       tc.Conversation,
		ArchitectIntent:    tc.intent(),
		WorkspaceAuthority: tc.WorkspaceStatus,
		Preflight:          tc.Preflight,
		Audit:              audit,
		Validation:         evidence,
		Diff:               diff,
	}
}

// inspectionPacket is what the reviewer receives for a read-only plan.
//
// It carries no diff because there is none, and no Sensei diff audit for the
// same reason: awareness_audit_diff judges a change, and there is nothing for
// it to judge. What remains is the plan, the evidence the run was given, and
// the findings the worker produced -- which is exactly the material needed to
// answer whether those findings are supported or merely asserted.
func inspectionPacket(tc taskContext, binding roles.Binding, start certifiedStart, plan, report string) roles.IndependentReviewPacket {
	return roles.IndependentReviewPacket{
		Provenance: roles.Provenance{
			TaskID: binding.TaskID, Role: roles.Reviewer,
			BaseSHA: binding.BaseSHA, CandidateDigest: binding.CandidateDigest,
			GraphBuildCommit: start.GraphBuildCommit(),
			SessionMode:      roles.Fresh,
			At:               time.Now().UTC(),
		},
		Task:               tc.Task,
		Plan:               plan,
		Conversation:       tc.Conversation,
		ArchitectIntent:    tc.intent(),
		WorkspaceAuthority: tc.WorkspaceStatus,
		Preflight:          tc.Preflight,
		Report:             report,
	}
}

// handoffPacket is what passes between implementers when one does not converge.
//
// The findings travel with it. A worker that inherits a candidate without the
// objections nobody answered will re-derive them, differently, and undo the
// previous worker's fix on its way past.
func handoffPacket(state taskstate.State, binding roles.Binding, previous string, graphBuildCommit string, cyclesUsed, cyclesAllowed int) roles.WorkerHandoffPacket {
	findings := make([]roles.Finding, 0, len(state.Open))
	for i, f := range state.Open {
		// Carried at major rather than blocking. These are objections the
		// previous cycle did not answer, not fresh ratings, and promoting them
		// would let an unanswered note refuse the next candidate on its own.
		findings = append(findings, roles.Finding{
			ID:        fmt.Sprintf("f%d", i+1),
			Severity:  roles.Major,
			Claim:     f.Detail,
			Reference: f.Source,
			Reason:    "left unanswered when " + previous + " handed the candidate on",
		})
	}
	return roles.WorkerHandoffPacket{
		Provenance: roles.Provenance{
			TaskID: binding.TaskID, Role: roles.Implementer, Provider: previous,
			BaseSHA: binding.BaseSHA, GraphBuildCommit: graphBuildCommit,
			SessionMode: roles.Fresh, At: time.Now().UTC(),
		},
		PreviousWorker: previous,
		State:          state.Handover(previous, graphBuildCommit),
		OpenFindings:   findings,
		CyclesUsed:     cyclesUsed,
		CyclesAllowed:  cyclesAllowed,
	}
}

// recordReconciliation writes the orchestration receipt for a disagreement.
//
// A receipt that cannot pass its own validation is reported as unrecorded
// rather than written anyway. The failure mode it guards against is a
// reconciliation that rests on nothing but agreement, and a receipt that
// silently downgrades itself to prose would be that failure with a paper trail.
func (e *Engine) recordReconciliation(taskID string, binding roles.Binding, r roles.Reconciliation) {
	r.Provenance = roles.Provenance{
		TaskID: taskID, Role: roles.Architect,
		Provider: e.Config.Architect.Name, SessionID: e.SessionID,
		BaseSHA: binding.BaseSHA, CandidateDigest: binding.CandidateDigest,
		At: time.Now().UTC(),
	}
	if err := r.Validate(); err != nil {
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status,
			"a disagreement was resolved without a recordable reconciliation: "+err.Error(), r))
		return
	}
	e.emit(event.New(e.SessionID, taskID, event.SourceArchitect, event.ArchitectReconciliation, r.Describe(), r))
}

// reconciliationEvidence is what an architect's resolution actually rests on.
// Sensei's audit and the executed checks are canonical; the architect's own
// summary is not, and is deliberately not listed as evidence for itself.
func reconciliationEvidence(audit, validation string, revised architectureDecision) []roles.Evidence {
	var out []roles.Evidence
	if a := strings.TrimSpace(audit); a != "" {
		out = append(out, roles.Evidence{Kind: roles.GraphEvidence, Reference: "awareness_audit_diff", Detail: oneLine(a)})
	}
	if v := strings.TrimSpace(validation); v != "" {
		out = append(out, roles.Evidence{Kind: roles.ProofEvidence, Reference: "broker validation", Detail: oneLine(v)})
	}
	for _, inv := range revised.Invariants {
		out = append(out, roles.Evidence{Kind: roles.GraphEvidence, Reference: inv})
	}
	for _, f := range revised.Files {
		out = append(out, roles.Evidence{Kind: roles.RepositoryEvidence, Reference: f})
	}
	return out
}

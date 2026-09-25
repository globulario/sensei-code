package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/sensei"
	"github.com/globulario/sensei-code/internal/taskstate"
	"github.com/globulario/sensei-code/internal/validation"
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
	// The WHOLE digest. It used to be truncated to sixteen hex characters --
	// 64 bits -- which was a fine short revision label and is not a thing to
	// carry as one leg of an exact {B, D, T} identity. shortDigest() exists for
	// display; evidence keeps its full width.
	return hex.EncodeToString(sum[:])
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
			CandidateTree:    binding.CandidateTree,
			GraphBuildCommit: start.GraphBuildCommit(),
			SessionMode:      roles.Fresh,
			At:               time.Now().UTC(),
		},
		Task:               tc.Task,
		Plan:               plan,
		PlanSource:         string(tc.PlanSource),
		PlanDigest:         tc.PlanDigest,
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
			CandidateTree:    binding.CandidateTree,
			GraphBuildCommit: start.GraphBuildCommit(),
			SessionMode:      roles.Fresh,
			At:               time.Now().UTC(),
		},
		Task:               tc.Task,
		Plan:               plan,
		PlanSource:         string(tc.PlanSource),
		PlanDigest:         tc.PlanDigest,
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
		// Reconciliation is the exceptional path -- two reviews disagreed and
		// the architect may let the accepting one stand -- so it is the LAST
		// artifact that should speak a smaller identity than the ordinary path.
		CandidateTree: binding.CandidateTree,
		At:            time.Now().UTC(),
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

// auditObservation is one diff-audit finding exactly as Sensei reported it.
type auditObservation = sensei.AuditFinding

// requiredTestRun is the execution broker's record of one named required test.
type requiredTestRun = validation.RequiredTest

// correlatedRequiredTest is one audit observation about a required test, read
// against the broker's execution of THAT SAME test at THIS candidate.
type correlatedRequiredTest struct {
	ID string
	// Observation is the audit finding, preserved as reported.
	Observation auditObservation
	// Run is the broker's record under exactly ID, or nil when there is none.
	Run       *requiredTestRun
	Satisfied bool
	State     string
	Reason    string
}

const (
	// requiredTestProvenPassing: the candidate did not edit the test, and the
	// broker executed it by name and it passed against this exact candidate.
	requiredTestProvenPassing = "not edited, proven passing"
	// requiredTestUnproven: the candidate did not edit the test, and nothing
	// the broker recorded proves it passed against this candidate.
	requiredTestUnproven = "not edited, not proven"
)

// correlateRequiredTests reads every required-test observation the diff audit
// made against the broker's record of THAT test at THIS candidate.
//
// The match is on the canonical required-test id and nothing else: not count,
// not position, not file. An observation is satisfied only when the record under
// its own id discharges it for candidateID at diffDigest; every other case --
// no record, not executed, failed, bound to other bytes -- leaves it outstanding
// with the reason. The observation itself is carried unaltered either way.
//
// No diff and no changed paths are taken: whether the candidate edited the
// test's file has no bearing on whether the test passed.
func correlateRequiredTests(findings []auditObservation, runs []requiredTestRun, candidateID, diffDigest string) []correlatedRequiredTest {
	byID := make(map[string]*requiredTestRun, len(runs))
	for i := range runs {
		byID[validation.CanonicalRequiredTestID(runs[i].ID)] = &runs[i]
	}
	var out []correlatedRequiredTest
	for _, f := range findings {
		if f.RecordClass != "required_test" {
			continue
		}
		c := correlatedRequiredTest{
			ID: validation.CanonicalRequiredTestID(f.RecordID), Observation: f,
			State: requiredTestUnproven,
		}
		run := byID[c.ID]
		switch {
		case c.ID == "":
			c.Reason = "the audit observation names no required-test id, so no execution can be matched to it"
		case run == nil:
			c.Reason = "the execution broker has no record of this required test for this candidate"
		case run.CandidateID != candidateID || run.DiffDigest != diffDigest || !run.Evidence.Certifies(candidateID, diffDigest):
			c.Run = run
			c.Reason = "the broker's record of this test is bound to other candidate content"
		case !run.Executed:
			c.Run = run
			c.Reason = "the broker did not observe this named test execute"
		case !run.Passed || run.Evidence.Outcome != validation.Passed:
			c.Run = run
			c.Reason = "the broker executed this named test and it failed"
		case run.Discharges(c.ID, candidateID, diffDigest):
			c.Run = run
			c.Satisfied, c.State = true, requiredTestProvenPassing
		default:
			c.Run = run
			c.Reason = "the broker's record of this test does not prove it passed against this candidate"
		}
		out = append(out, c)
	}
	return out
}

// renderRequiredTestEvidence writes the correlation for a reviewer. The two
// states read differently on purpose: a reviewer must be able to answer "was
// this required test satisfied" without inferring it from the diff.
func renderRequiredTestEvidence(correlated []correlatedRequiredTest, candidateID, diffDigest string) string {
	if len(correlated) == 0 {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "REQUIRED-TEST OBSERVATIONS from the Sensei diff audit, matched by required-test id to the broker's execution at candidate %s, diff %s:\n",
		candidateID, shortDigest(diffDigest))
	for _, c := range correlated {
		if c.Satisfied {
			fmt.Fprintf(&sb, "  SATISFIED    %s — %s: the execution broker ran it by name against this exact candidate and it passed\n", c.ID, c.State)
		} else {
			fmt.Fprintf(&sb, "  OUTSTANDING  %s — %s: %s\n", c.ID, c.State, c.Reason)
		}
		explanation := c.Observation.Explanation
		if explanation == "" {
			explanation = c.Observation.Detail
		}
		fmt.Fprintf(&sb, "               audit observation, preserved: [%s] %s\n", c.Observation.Disposition, explanation)
	}
	sb.WriteString("A required test is discharged by executing and passing against this candidate, not by appearing in the diff;\n")
	sb.WriteString("editing the required test's file is not required, and an outstanding one stays outstanding until it is proven.")
	return sb.String()
}

// reviewValidationEvidence is the validation text the independent reviewer
// reads: the bundle, the broker's per-test records, and their correlation with
// the audit's observations.
func reviewValidationEvidence(bundle string, runs []requiredTestRun, correlated []correlatedRequiredTest, candidateID, diffDigest string) string {
	var parts []string
	for _, p := range []string{bundle, validation.RenderRequiredTests(runs), renderRequiredTestEvidence(correlated, candidateID, diffDigest)} {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "\n\n")
}

// requiredTestResults projects the correlation into task evidence.
func requiredTestResults(correlated []correlatedRequiredTest) []taskstate.RequiredTestResult {
	var out []taskstate.RequiredTestResult
	for _, c := range correlated {
		r := taskstate.RequiredTestResult{ID: c.ID, Satisfied: c.Satisfied, State: c.State}
		if c.Run != nil {
			r.Executed, r.Passed = c.Run.Executed, c.Run.Passed
		}
		out = append(out, r)
	}
	return out
}

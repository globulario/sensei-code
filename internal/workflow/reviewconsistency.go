package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/sensei"
	"github.com/globulario/sensei-code/internal/validation"
)

// Review consistency across reviewer substitution.
//
// Observed (B3 N2b, 2026-08-28, candidate 8f58b4c0783b): cycle 1's reviewer
// returned REVISE because a proof the plan required was absent. The worker
// did not converge, the candidate passed to the next worker by handoff, and
// because that worker had been the reviewer, the reviewer role passed to the
// provider that had been the worker. Cycle 2 reviewed the identical diff
// digest, the identical validation outcomes and the identical audit, in a
// session that inherits nothing -- and returned ACCEPT without mentioning the
// finding. Nothing had changed but who was asked. The run concluded.
//
// The reviewer's independence is not the defect: a fresh session is what
// makes a review a review. The defect is that the engine, which had recorded
// the finding, let the next verdict replace it instead of answer it. So the
// engine keeps the open findings of a non-accepting verdict, bound to the
// exact candidate and evidence they were raised against, and an ACCEPT that
// arrives on the same candidate and the same evidence is a contradiction
// between two reviews -- adjudicated by the architect, on the record, never
// resolved by whichever verdict came last.
//
// Deliberately NOT encoded: "the first reviewer wins", "REVISE is permanent",
// or any ordering of providers. A later reviewer may disagree. Disagreement
// becomes a first-class reconciliation with both positions in it.

// openReview is the unanswered part of a non-accepting verdict, held by the
// engine per task until the candidate or its evidence changes, or an
// adjudication closes it.
type openReview struct {
	Reviewer         string          `json:"reviewer"`
	Attempt          int             `json:"review_attempt"`
	Decision         roles.Decision  `json:"decision"`
	Summary          string          `json:"summary"`
	Findings         []roles.Finding `json:"findings,omitempty"`
	CandidateDigest  string          `json:"candidate_digest"`
	CandidateTree    string          `json:"candidate_tree,omitempty"`
	EvidenceIdentity string          `json:"evidence_identity"`
}

// evidenceIdentity names the evidence a verdict was reached on: every executed
// check's kind, command, outcome and exit status, and the audit's decision,
// availability and digest.
//
// Output digests are deliberately excluded: `go test` prints its timings, so
// two runs of the same checks on the same bytes produce different output and
// would read as different evidence. What a reviewer is entitled to change its
// mind on is an outcome, not a millisecond count.
func evidenceIdentity(b validation.Bundle, audit sensei.DiffAuditDecision) string {
	lines := make([]string, 0, len(b.Checks)+1)
	for _, c := range b.Checks {
		lines = append(lines, strings.Join([]string{string(c.Kind), c.Command, strings.Join(c.Args, " "), string(c.Outcome), fmt.Sprint(c.ExitStatus)}, "\x1f"))
	}
	sort.Strings(lines)
	lines = append(lines, "audit\x1f"+string(audit.Decision)+"\x1f"+string(audit.Availability)+"\x1f"+audit.Digest)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// openReviewFrom records what a non-accepting verdict left unanswered.
// openReviewFrom records a verdict left open, in the same identity language as
// everything else: the contradiction record names the content, not only the
// representation of it.
func openReviewFrom(v roles.ReviewVerdict, attempt int, candidateDigest, evidenceID string) openReview {
	return openReview{
		Reviewer:         v.Provenance.Provider,
		Attempt:          attempt,
		Decision:         v.Decision,
		Summary:          v.Summary,
		Findings:         v.Findings,
		CandidateDigest:  candidateDigest,
		CandidateTree:    v.Provenance.CandidateTree,
		EvidenceIdentity: evidenceID,
	}
}

// contradicts reports whether an accepting verdict on this candidate and
// evidence contradicts the open review rather than answering it.
//
// "The same candidate" is decided by the CONTENT as well as the representation:
// the digest names a rendering, and the tree names the bytes. Requiring both to
// match makes this predicate mean what its own "exact candidate" language
// claims.
//
// The candidate or the evidence changing at all is taken as the mechanical
// resolution: the finding was raised against bytes and outcomes that no longer
// exist, and the new verdict is about new facts. That is a deliberately coarse
// reading -- a one-character edit clears a proof-gap finding -- and it is the
// conservative side: this check refuses to let a verdict flip on NOTHING, and
// does not try to judge whether a change was enough.
func (o openReview) contradicts(accepting roles.ReviewVerdict, candidateDigest, evidenceID string) bool {
	return accepting.Accepts() &&
		o.CandidateDigest != "" && o.CandidateDigest == candidateDigest &&
		o.EvidenceIdentity != "" && o.EvidenceIdentity == evidenceID &&
		o.CandidateTree == accepting.Provenance.CandidateTree
}

// describe is the contradiction stated once, for the event and the receipt.
func (o openReview) describe(accepting roles.ReviewVerdict) string {
	return fmt.Sprintf("review contradiction on an unchanged candidate: %s (attempt %d) did not accept and %s (attempt %d) accepted the same candidate digest %s on the same evidence; nothing changed but the reviewer. Open: %s",
		o.Reviewer, o.Attempt, accepting.Provenance.Provider, o.Attempt+1, shortDigest(o.CandidateDigest), oneLine(o.Summary))
}

// contradictionPrompt puts the two verdicts to the architect for adjudication.
func contradictionPrompt(task, plan, audit string, o openReview, accepting roles.ReviewVerdict) string {
	var findings strings.Builder
	for _, f := range o.Findings {
		findings.WriteString("  " + f.Line() + "\n")
	}
	return fmt.Sprintf(`Two independent reviews of the SAME candidate revision, on the SAME executed evidence, disagree. Nothing about the candidate changed between them; only the reviewer did. Adjudicate using your architectural authority: decide whether the earlier findings stand, and issue a revised bounded plan that either requires them to be answered or records why they do not apply. Escalate to the human only if the decision changes human-owned intent/policy/contract/trust authority.

TASK:
%s

CURRENT PLAN:
%s

SENSEI AUDIT:
%s

EARLIER REVIEW (%s, did not accept):
%s
%s
ACCEPTING REVIEW (%s):
%s

Return ONLY the same architecture JSON contract as before, with one additional field: "adjudication": "revise" if the earlier finding applies and the revised plan says what is still owed, or "adjudication": "accepting_review_stands" if the earlier finding does not apply to this candidate and no edit is owed.`, task, plan, audit, o.Reviewer, o.Summary, findings.String(), accepting.Provenance.Provider, accepting.Summary)
}

// Adjudication vocabulary. The architect answers a review contradiction with
// exactly one of these; the engine reads the answer by membership.
const (
	// adjudicationRevise: the earlier finding applies; the revised plan says
	// what the candidate still owes.
	adjudicationRevise = "revise"
	// adjudicationStands: the earlier finding does not apply to this
	// candidate; the accepting review stands and no edit is owed.
	adjudicationAcceptingStands = "accepting_review_stands"
)

// adjudicationStands reads the architect's answer. Silence is "revise": an
// architect that did not say the accepting review stands has not said it,
// and the conservative reading keeps the candidate in revision.
func adjudicationStands(d architectureDecision) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(d.Adjudication)) {
	case "", adjudicationRevise:
		return false, nil
	case adjudicationAcceptingStands:
		return true, nil
	default:
		return false, fmt.Errorf("the architect answered the review contradiction with %q, which is neither %q nor %q", d.Adjudication, adjudicationRevise, adjudicationAcceptingStands)
	}
}

// Engine-side state. Per task, not per worker: the whole point is that it
// survives the handoff that substitutes the reviewer.

func (e *Engine) setOpenReview(taskID string, o openReview) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.openReviews == nil {
		e.openReviews = make(map[string]openReview)
	}
	e.openReviews[taskID] = o
}

func (e *Engine) openReview(taskID string) (openReview, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	o, ok := e.openReviews[taskID]
	return o, ok
}

func (e *Engine) clearOpenReview(taskID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.openReviews, taskID)
}

// nextReviewAttempt is the encounter-level review counter. The cycle counter
// restarts with every worker; this one does not, so a reader of the record
// can count reviews across a handoff without counting candidates twice.
func (e *Engine) nextReviewAttempt(taskID string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.reviewAttempts == nil {
		e.reviewAttempts = make(map[string]int)
	}
	e.reviewAttempts[taskID]++
	return e.reviewAttempts[taskID]
}

func (e *Engine) reviewAttempt(taskID string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reviewAttempts[taskID]
}

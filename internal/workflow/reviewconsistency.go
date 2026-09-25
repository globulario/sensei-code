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
	// Discharged is the per-finding accounting so far, keyed by finding id:
	// what discharged each finding that has been answered. A finding absent
	// from it is still outstanding, across cycles and across a handoff.
	Discharged map[string]string `json:"discharged,omitempty"`
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

// Per-finding accounting.
//
// LAW: the class of a finding binds the class of its response. Measured on the
// DF-19 resume: a review returned a blocking CODE finding and a major EVIDENCE
// finding; the implementer answered the evidence one, declared the cycle
// "evidence-only", and nothing objected. The identical-diff check caught the
// symptom, but it is a proxy -- one unrelated changed line would have carried
// the code defect forward as answered.
//
// So convergence is decided here, per finding and by id, before any reviewer
// is asked again:
//   - the class is the finding record's, never the respondent's;
//   - a CODE finding is discharged only by a code response on a candidate
//     whose content actually changed; an EVIDENCE finding only by an evidence
//     response carrying the evidence; a SCOPE finding only by a scope response;
//   - a finding no response names is outstanding, whatever the diff did;
//   - a respondent that disagrees with a finding's class says so, and the
//     disagreement is routed for adjudication. It discharges nothing.

// responseKind is what a respondent claims its answer to one finding is.
type responseKind string

const (
	respondCode     responseKind = "code"
	respondEvidence responseKind = "evidence"
	respondScope    responseKind = "scope"
	// respondDisagree is the respondent saying the finding is wrong or
	// misclassified. It is input for adjudication, never a discharge.
	respondDisagree responseKind = "disagree"
)

// responsePrefix opens one accounting line in the respondent's report:
//
//	RESPONSE <finding id> <code|evidence|scope|disagree>: <what answers it>
const responsePrefix = "RESPONSE "

// findingResponse is one accounting line, exactly as the respondent wrote it.
type findingResponse struct {
	ID     string
	Kind   responseKind
	Detail string
}

// parseFindingResponses reads the accounting lines out of a respondent's
// report. The kind is kept as written; whether it is a known kind, and whether
// it can discharge the finding it names, is decided by account, not here.
func parseFindingResponses(report string) []findingResponse {
	var out []findingResponse
	for _, line := range strings.Split(report, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, responsePrefix) {
			continue
		}
		head, detail, _ := strings.Cut(strings.TrimPrefix(line, responsePrefix), ":")
		fields := strings.Fields(head)
		if len(fields) != 2 {
			continue
		}
		out = append(out, findingResponse{ID: fields[0], Kind: responseKind(fields[1]), Detail: strings.TrimSpace(detail)})
	}
	return out
}

// outstandingFinding is a finding this response did not discharge, and why.
type outstandingFinding struct {
	Finding roles.Finding `json:"finding"`
	Why     string        `json:"why"`
}

// findingAccounting is one response set against every outstanding finding.
type findingAccounting struct {
	// Discharged is the accounting after this response: every earlier
	// discharge plus the ones this response established.
	Discharged map[string]string `json:"discharged,omitempty"`
	// Answered counts the findings this response discharged.
	Answered int `json:"answered"`
	// Open are findings no compatible response discharged.
	Open []outstandingFinding `json:"open,omitempty"`
	// Disputed are findings the respondent disagreed with. They are routed
	// for adjudication and remain outstanding.
	Disputed []outstandingFinding `json:"disputed,omitempty"`
}

// converged is true only when every outstanding finding was discharged.
func (a findingAccounting) converged() bool { return len(a.Open) == 0 && len(a.Disputed) == 0 }

// account sets one response against every finding the open review holds.
// Severity grants no exemption: a minor finding is outstanding by id until a
// response of its class discharges it, exactly like any other.
//
// candidateTree is the content identity of the candidate the response
// produced. A code response discharges a CODE finding only if that content
// differs from the content the finding was raised against: a code answer that
// changed nothing is not a code answer.
//
// The class read is the finding record's. The respondent's kind is compared
// with it and never substituted for it.
func (o openReview) account(responses []findingResponse, candidateTree string) findingAccounting {
	acct := findingAccounting{Discharged: map[string]string{}}
	for id, how := range o.Discharged {
		acct.Discharged[id] = how
	}
	byID := map[string][]findingResponse{}
	for _, r := range responses {
		byID[r.ID] = append(byID[r.ID], r)
	}
	for _, f := range o.Findings {
		if _, done := acct.Discharged[f.ID]; done {
			continue
		}
		rs := byID[f.ID]
		if len(rs) == 0 {
			acct.Open = append(acct.Open, outstandingFinding{Finding: f, Why: "no response named it"})
			continue
		}
		if r, ok := firstOfKind(rs, respondDisagree); ok {
			// Checked before any discharge: a respondent that disagrees and
			// also claims an answer has not settled which it means.
			acct.Disputed = append(acct.Disputed, outstandingFinding{Finding: f,
				Why: "the respondent disagrees with this " + string(f.Class) + " finding (" + orNone(r.Detail, "no reason stated") + "); a disagreement is escalated and discharges nothing"})
			continue
		}
		how, why := discharge(f, rs, o.CandidateTree, candidateTree)
		if how == "" {
			acct.Open = append(acct.Open, outstandingFinding{Finding: f, Why: why})
			continue
		}
		acct.Discharged[f.ID] = how
		acct.Answered++
	}
	return acct
}

// discharge decides whether any of the responses naming a finding is one its
// class accepts. It returns how the finding was discharged, or why it was not.
func discharge(f roles.Finding, rs []findingResponse, raisedOn, now string) (string, string) {
	why := ""
	for _, r := range rs {
		switch {
		case r.Kind != respondCode && r.Kind != respondEvidence && r.Kind != respondScope:
			why = fmt.Sprintf("the response kind %q is not code, evidence, scope, or disagree", r.Kind)
		case string(r.Kind) != string(f.Class):
			// The reclassification refusal. The finding record says what kind
			// of answer it needs; the respondent's reading is not authority.
			why = fmt.Sprintf("a %s response does not discharge a %s finding; the class is the finding's, not the respondent's", r.Kind, orNone(string(f.Class), "no class"))
		case r.Detail == "":
			why = fmt.Sprintf("the %s response states nothing that answers it", r.Kind)
		case r.Kind == respondCode && (strings.TrimSpace(raisedOn) == "" || strings.TrimSpace(now) == ""):
			why = "a code response cannot be checked: the candidate's content identity is not known on both sides"
		case r.Kind == respondCode && raisedOn == now:
			why = "a code response on a candidate whose content did not change since the finding was raised"
		default:
			return string(r.Kind) + ": " + r.Detail, ""
		}
	}
	return "", why
}

func firstOfKind(rs []findingResponse, k responseKind) (findingResponse, bool) {
	for _, r := range rs {
		if r.Kind == k {
			return r, true
		}
	}
	return findingResponse{}, false
}

// describe names every finding still outstanding, by id, with why.
func (a findingAccounting) describe() string {
	var parts []string
	for _, o := range a.Open {
		parts = append(parts, fmt.Sprintf("[%s] %s: %s", o.Finding.ID, orNone(string(o.Finding.Class), "no class"), o.Why))
	}
	for _, o := range a.Disputed {
		parts = append(parts, fmt.Sprintf("[%s] %s: %s", o.Finding.ID, orNone(string(o.Finding.Class), "no class"), o.Why))
	}
	return fmt.Sprintf("%d review finding(s) are not accounted for, so the cycle has not converged: %s",
		len(a.Open)+len(a.Disputed), strings.Join(parts, "; "))
}

// responseContract is what the respondent is told it owes: each outstanding
// finding with the class its record carries, and the one grammar an answer is
// read in.
func (o openReview) responseContract() string {
	var b strings.Builder
	for _, f := range o.Findings {
		if _, done := o.Discharged[f.ID]; done {
			continue
		}
		b.WriteString("  - [" + f.ID + "] class " + string(f.Class) + ": " + oneLine(f.Claim) + "\n")
	}
	if b.Len() == 0 {
		return ""
	}
	return `FINDING ACCOUNTING -- every finding below must be answered by id, in your final message, one line each:
  RESPONSE <id> <code|evidence|scope|disagree>: <what answers it>
The class is the finding's and it decides which answer discharges it: a code finding only by a change to
the candidate, an evidence finding only by the evidence itself, a scope finding only by a scope answer.
If you believe a finding is wrong or misclassified, answer "disagree" with your reason: that is escalated
for adjudication and does NOT discharge the finding. A finding you do not name is still open.
Outstanding:
` + strings.TrimRight(b.String(), "\n")
}

// disputePrompt routes a respondent's disagreement with findings to the
// architect. The architect may revise the plan; nothing it says discharges a
// finding, because a finding is discharged only by a response of its class.
func disputePrompt(task, plan, audit string, o openReview, disputed []outstandingFinding) string {
	var b strings.Builder
	for _, d := range disputed {
		b.WriteString("  " + d.Finding.Line() + "\n    respondent: " + d.Why + "\n")
	}
	return fmt.Sprintf(`The implementer disagrees with review findings raised by %s (attempt %d). A disagreement is an escalation, not a discharge: each finding below stays open, with the class its record carries, until a response of that class answers it. Using your architectural authority, issue a revised bounded plan that says how each disputed finding is to be answered. Escalate to the human only if the decision changes human-owned intent/policy/contract/trust authority.

TASK:
%s

CURRENT PLAN:
%s

SENSEI AUDIT:
%s

DISPUTED FINDINGS:
%s
Return ONLY the same architecture JSON contract as before.`, o.Reviewer, o.Attempt, task, plan, audit, b.String())
}

// contradicts reports whether an accepting verdict on this candidate and
// evidence contradicts the open review rather than answering it.
//
// "The same candidate" is decided by the CONTENT as well as the representation:
// the digest names a rendering, and the tree names the bytes. Requiring both to
// match makes this predicate mean what its own "exact candidate" language
// claims.
//
// This is not what answers a finding. Whether each finding was answered is
// decided by account, per finding and by class, before the reviewer is asked
// again; a changed candidate or changed evidence answers nothing by itself.
// This check only refuses a verdict that flips on NOTHING when no accounting
// has closed the open review.
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

// Implementer eligibility after a failed independent-review leg.
//
// Recorded per task AND PER CANDIDATE. The exclusion is a fact about one
// candidate's independence -- "this participant could not judge these bytes" --
// not a standing judgement about the provider. Keying it by task alone would
// have made a provider that failed one candidate's review leg permanently
// ineligible to implement every later candidate in that task, which retires a
// worker for a transport failure it had no part in.
func (e *Engine) excludeFromImplementing(taskID string, u *roles.ReviewUnobtainable) {
	if u == nil {
		return
	}
	digest := strings.TrimSpace(u.Binding.CandidateDigest)
	if digest == "" {
		// Without a candidate identity the exclusion cannot be attributed, and an
		// unattributable exclusion is the task-wide one this scoping exists to
		// prevent. Nothing is recorded.
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.reviewLegFailures == nil {
		e.reviewLegFailures = map[string]map[string]*roles.ReviewUnobtainable{}
	}
	if e.reviewLegFailures[taskID] == nil {
		e.reviewLegFailures[taskID] = map[string]*roles.ReviewUnobtainable{}
	}
	e.reviewLegFailures[taskID][digest] = u
}

// implementerExcluded reports whether a participant failed the independent-review
// leg for THIS EXACT candidate, and why.
//
// Read by MEMBERSHIP of the recorded failures for that one candidate digest. An
// empty digest excludes nobody: a candidate that cannot be named cannot be the
// candidate somebody failed to review, and guessing would re-create the
// task-wide exclusion.
func (e *Engine) implementerExcluded(taskID, participant, candidateDigest string) (string, bool) {
	digest := strings.TrimSpace(candidateDigest)
	if digest == "" {
		return "", false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	byCandidate, ok := e.reviewLegFailures[taskID]
	if !ok {
		return "", false
	}
	u, ok := byCandidate[digest]
	if !ok || u == nil || !u.Excludes(participant) {
		return "", false
	}
	return "it failed the independent-review leg for candidate " +
		shortDigest(u.Binding.CandidateDigest) + ", and the participant that could not judge a candidate " +
		"does not become its implementer", true
}

// continuingCandidate is the candidate a resumed or handed-on worker would take
// over, if the engine recorded a failed review leg for one.
//
// It exists so the eligibility check can name a candidate at SELECTION time,
// before this iteration has produced one. Exactly one recorded candidate makes
// the answer unambiguous; more than one does not, and an ambiguous answer must
// not be guessed into a task-wide exclusion.
func (e *Engine) continuingCandidate(taskID string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	byCandidate := e.reviewLegFailures[taskID]
	if len(byCandidate) != 1 {
		return ""
	}
	for digest := range byCandidate {
		return digest
	}
	return ""
}

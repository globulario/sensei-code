package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
// This is NOT how an open review is answered. It used to be: the candidate or
// the evidence changing at all was taken as the mechanical resolution, so a
// one-line unrelated edit released a blocking code finding nobody had touched.
// An open review is now answered finding by finding (see reconcile), and this
// predicate is only the refusal to let a verdict flip on NOTHING.
func (o openReview) contradicts(accepting roles.ReviewVerdict, candidateDigest, evidenceID string) bool {
	return accepting.Accepts() &&
		o.CandidateDigest != "" && o.CandidateDigest == candidateDigest &&
		o.EvidenceIdentity != "" && o.EvidenceIdentity == evidenceID &&
		o.CandidateTree == accepting.Provenance.CandidateTree
}

// findingAccounting is the per-finding answer to an open review: which
// findings the implementer's responses discharged, which are still open and
// why, and which the implementer disputed.
type findingAccounting struct {
	Discharged []string
	Open       []openFinding
	// Disputed are the disagreements the implementer raised. Every disputed
	// finding is also in Open: a disagreement is a route, not a discharge.
	Disputed []roles.FindingResponse
}

// openFinding is one finding the accounting could not close, named by id.
type openFinding struct {
	ID     string
	Class  roles.FindingClass
	Reason string
}

// Converged is the convergence predicate: every outstanding finding was
// individually accounted for. It is decided here, per finding, and never by
// whether the candidate's diff moved.
func (a findingAccounting) Converged() bool { return len(a.Open) == 0 }

// OpenIDs names what is still owed, in finding order.
func (a findingAccounting) OpenIDs() []string {
	ids := make([]string, 0, len(a.Open))
	for _, f := range a.Open {
		ids = append(ids, f.ID)
	}
	return ids
}

// describe states every open finding by id, class and reason.
func (a findingAccounting) describe() string {
	parts := make([]string, 0, len(a.Open))
	for _, f := range a.Open {
		parts = append(parts, fmt.Sprintf("[%s] %s: %s", f.ID, strings.ToUpper(string(f.Class)), f.Reason))
	}
	return strings.Join(parts, "; ")
}

// reconcile accounts for every outstanding finding of the open review against
// the implementer's per-finding responses, on the candidate and evidence the
// cycle actually produced.
//
// The class compared is ALWAYS the finding's. Beyond the response naming that
// class, each class is discharged only by work of its own kind:
//
//   - CODE and SCOPE need the candidate to have changed since the finding was
//     raised. A code defect is not answered by a paragraph, and a change out of
//     bound is not brought back inside it without changing the change.
//   - EVIDENCE needs the executed check the response names, passed and bound
//     to this candidate. It does NOT need a code change: a proof gap answered
//     by proof leaves the diff exactly where it was, and that is the correct
//     shape. It is the NAMED check that must have run: some other check
//     passing is not the proof this answer claims.
//
// Minor findings are not outstanding; they do not affect the decision.
// A finding answered more than once is open: two accounts of one finding are
// not an account of it.
func (o openReview) reconcile(responses []roles.FindingResponse, candidateDigest string, evidence validation.Bundle) findingAccounting {
	byID := map[string][]roles.FindingResponse{}
	for _, r := range responses {
		id := strings.TrimSpace(r.FindingID)
		byID[id] = append(byID[id], r)
	}
	var a findingAccounting
	for _, f := range o.Findings {
		if f.Severity == roles.Minor {
			continue
		}
		stillOpen := func(reason string) { a.Open = append(a.Open, openFinding{ID: f.ID, Class: f.Class, Reason: reason}) }
		answers := byID[strings.TrimSpace(f.ID)]
		switch {
		case len(answers) == 0:
			stillOpen("no response accounts for it")
			continue
		case len(answers) > 1:
			stillOpen(fmt.Sprintf("answered %d times; one finding takes one account", len(answers)))
			continue
		}
		r := answers[0]
		if r.Response == roles.ResponseDisagree {
			a.Disputed = append(a.Disputed, r)
			stillOpen("disputed by the implementer and escalated; a disagreement does not discharge it: " + oneLine(r.Disagreement))
			continue
		}
		if err := r.Discharges(f); err != nil {
			stillOpen(err.Error())
			continue
		}
		switch f.Class {
		case roles.ClassCode, roles.ClassScope:
			if o.CandidateDigest == "" || candidateDigest == "" || o.CandidateDigest == candidateDigest {
				stillOpen("declared answered, but the candidate did not change since the finding was raised")
				continue
			}
		case roles.ClassEvidence:
			if strings.TrimSpace(r.Check) == "" {
				stillOpen("declared answered by evidence, but the response names no executed check it rests on")
				continue
			}
			if !executedFor(evidence, candidateDigest, r.Check) {
				stillOpen(fmt.Sprintf("declared answered by evidence, but the named check %q did not execute and pass against this candidate", strings.TrimSpace(r.Check)))
				continue
			}
		}
		a.Discharged = append(a.Discharged, f.ID)
	}
	return a
}

// executedFor reports whether the bundle holds the named check, executed and
// passed against exactly this candidate. The name is compared as the command
// line the broker ran, exactly: a check that merely resembles it is another
// check.
func executedFor(b validation.Bundle, candidateDigest, check string) bool {
	check = strings.TrimSpace(check)
	if candidateDigest == "" || check == "" || b.DiffDigest != candidateDigest {
		return false
	}
	for _, c := range b.Checks {
		if c.DiffDigest == candidateDigest && c.Outcome == validation.Passed && commandLine(c) == check {
			return true
		}
	}
	return false
}

// commandLine is a check as the broker ran it, in the form the validation
// evidence renders it.
func commandLine(c validation.Evidence) string {
	return strings.TrimSpace(c.Command + " " + strings.Join(c.Args, " "))
}

// findingResponsesKey is the one field the implementer's accounting arrives
// under. The decoder looks for it by name rather than taking the first JSON
// object in the message, because an implementer's report quotes code.
const findingResponsesKey = "finding_responses"

// decodeFindingResponses reads the implementer's per-finding accounting from
// its final message: the LAST object carrying finding_responses.
//
// Absent is not malformed, and neither is an empty answer: both return no
// responses, and reconcile holds every outstanding finding open by id.
func decodeFindingResponses(text string) ([]roles.FindingResponse, error) {
	at := strings.LastIndex(text, `"`+findingResponsesKey+`"`)
	if at < 0 {
		return nil, nil
	}
	start := strings.LastIndex(text[:at], "{")
	if start < 0 {
		return nil, errors.New("the implementer's finding_responses is not inside a JSON object")
	}
	var wire struct {
		Responses []roles.FindingResponse `json:"finding_responses"`
	}
	if err := json.NewDecoder(strings.NewReader(text[start:])).Decode(&wire); err != nil {
		return nil, fmt.Errorf("decode the implementer's finding_responses: %w", err)
	}
	for i := range wire.Responses {
		r := &wire.Responses[i]
		r.Response = roles.ResponseKind(strings.ToLower(strings.TrimSpace(string(r.Response))))
		r.Class = roles.FindingClass(strings.ToLower(strings.TrimSpace(string(r.Class))))
	}
	return wire.Responses, nil
}

// responsesPrompt asks the implementer to account for every outstanding
// finding by id. The finding's class is printed and the implementer is told it
// is not theirs to change.
func (o openReview) responsesPrompt() string {
	var b strings.Builder
	for _, f := range o.Findings {
		if f.Severity == roles.Minor {
			continue
		}
		b.WriteString("  - " + f.ID + " (" + strings.ToUpper(string(f.Class)) + ")\n")
	}
	if b.Len() == 0 {
		return ""
	}
	return `

FINDING ACCOUNTING -- every outstanding finding must be answered by id:
` + b.String() + `Each finding's class is fixed by the finding. A CODE finding is answered only by a code change,
an EVIDENCE finding only by a check the validation broker executed and passed on this candidate,
named in "check" exactly as the validation evidence shows its command line, a SCOPE finding only by bringing the change
back inside its bound. If you believe a finding is wrong or misclassified, answer it with
"disagree" and say why: that is escalated, and the finding stays open. Do not answer a finding
as a different class; that answer is refused. A finding you do not answer stays open.
End your final message with exactly one JSON object, and nothing after it:
{"` + findingResponsesKey + `":[{"finding_id":"f1","response":"discharge"|"disagree","class":"code"|"evidence"|"scope","account":"what you changed, or the command you ran and its outcome","check":"for evidence only: the executed check's command line","disagreement":"why the finding does not stand as recorded, only for disagree"}]}`
}

// disagreementPrompt puts an implementer's dispute of recorded findings to the
// architect. The findings stay open whatever the architect answers: a revised
// plan may say what is still owed, and the next cycle must account for it.
func disagreementPrompt(task, plan, audit string, o openReview, disputed []roles.FindingResponse) string {
	var b strings.Builder
	for _, r := range disputed {
		for _, f := range o.Findings {
			if f.ID == r.FindingID {
				b.WriteString("  " + f.Line() + "\n    implementer disputes it: " + oneLine(r.Disagreement) + "\n")
			}
		}
	}
	return fmt.Sprintf(`The implementer disputes review findings instead of answering them. A dispute is not a discharge: the findings remain open, with the class the finding records. Resolve the dispute using your architectural authority and issue a revised bounded plan that says what the candidate still owes for each disputed finding. Escalate to the human only if the decision changes human-owned intent/policy/contract/trust authority.

TASK:
%s

CURRENT PLAN:
%s

SENSEI AUDIT:
%s

REVIEW (%s):
%s

DISPUTED FINDINGS:
%s
Return ONLY the same architecture JSON contract as before.`, task, plan, audit, o.Reviewer, o.Summary, b.String())
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

package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/globulario/sensei-code/internal/report"
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
	// Files is the per-file identity of the candidate the findings were raised
	// on, so a response claiming a code change can be checked against a file
	// that actually moved since -- across a handoff too.
	Files map[string]string `json:"files,omitempty"`
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

// Finding-owned convergence.
//
// LAW: the class of a finding binds the class of its response. Measured
// 2026-09-25 on the DF-19 resume: a review returned a CODE finding and an
// EVIDENCE finding, the implementer answered the evidence one and declared the
// review "evidence-only", and the code defect was carried forward as answered.
// The engine refused only because the diff happened not to move; one unrelated
// edited line and the identical-diff check would have passed.
//
// So every outstanding finding is accounted for BY ID before the candidate goes
// back to review. The class is read from the finding record and nowhere else:
// the responder's own reading of a finding is input, and a response of another
// class is a refused reclassification, not a discharge. A responder that thinks
// a finding is misclassified says so as "disagree", which is escalated and
// leaves the finding open. Silence leaves it open.
//
// A response that passes this accounting has ANSWERED its finding, not
// discharged it. The accounting can establish that a code response points at a
// file that really moved, and that an evidence response cites checks the
// validation broker really ran against this candidate. It cannot establish that
// the movement is the repair the finding asked for -- an unrelated line in the
// named file moves that file too -- nor that a check answers the proof gap. So
// an answered finding goes back to review as the reviewer raised it, and only
// that review, naming its id as resolved, discharges it.

// findingResponseKind is what a response claims to be. The first three are the
// finding classes; a response discharges only a finding of its own class.
type findingResponseKind string

const (
	respondCode     findingResponseKind = "code"
	respondEvidence findingResponseKind = "evidence"
	respondScope    findingResponseKind = "scope"
	// respondDisagree disputes the finding's class. It is a route to the
	// architect, never a discharge.
	respondDisagree findingResponseKind = "disagree"
)

// findingResponse is one responder's account of one finding. It has no class
// field on purpose: a responder cannot state a finding's class, only its own
// reading of it (ClaimedClass), which is recorded as input and decides nothing.
type findingResponse struct {
	ID       string              `json:"id"`
	Response findingResponseKind `json:"response"`
	// Paths are the candidate files the response changed for this finding.
	Paths []string `json:"paths,omitempty"`
	// Executions name, by command line, the checks the validation broker ran
	// against this candidate that answer an evidence finding. They are
	// references to execution records the workflow produced, not a report:
	// one that names no such record answers nothing.
	Executions []string `json:"executions,omitempty"`
	// Evidence is the responder's own account of what it ran. It is prose, and
	// prose answers no finding.
	Evidence     string `json:"evidence,omitempty"`
	ClaimedClass string `json:"claimed_class,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

// findingResponsesMarker opens the one line of the implementer's output that
// carries its per-finding accounting.
const findingResponsesMarker = "FINDING-RESPONSES:"

// parseFindingResponses reads the LAST marker line of the responder's output.
// No line is silence on every finding. A line that does not decode exactly --
// including one that tries to carry a field the contract does not have -- is an
// error, and the caller treats it as silence too: a malformed account accounts
// for nothing.
func parseFindingResponses(text string) ([]findingResponse, error) {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, findingResponsesMarker) {
			continue
		}
		var body struct {
			Responses []findingResponse `json:"responses"`
		}
		dec := json.NewDecoder(strings.NewReader(strings.TrimPrefix(line, findingResponsesMarker)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			return nil, fmt.Errorf("the %s line does not decode: %v", findingResponsesMarker, err)
		}
		return body.Responses, nil
	}
	return nil, nil
}

// findingStatus is where one finding stands after a response cycle.
type findingStatus string

const (
	// findingAnswered: a response of the finding's class that points at a
	// change or an execution the workflow can see. It goes to the finding's
	// reviewer, and it is still open until that reviewer resolves it.
	findingAnswered findingStatus = "answered"
	// findingDischarged: the reviewer named the answered finding resolved.
	findingDischarged findingStatus = "discharged"
	// findingSilent: no response names the finding.
	findingSilent findingStatus = "silent"
	// findingReclassified: the response is of another class than the finding.
	findingReclassified findingStatus = "reclassification_refused"
	// findingUnattributed: the response is of the right class and points at
	// nothing that answers it -- no changed file, no execution the broker ran.
	findingUnattributed findingStatus = "unattributed"
	// findingAmbiguous: more than one response names the finding.
	findingAmbiguous findingStatus = "answered_more_than_once"
	// findingDisputed: the responder disputes the class. Escalated, open.
	findingDisputed findingStatus = "disputed"
	// findingUnclassified: the finding record carries no valid class, so no
	// response can be checked against it. Its class is never inferred.
	findingUnclassified findingStatus = "unclassified"
)

// findingAccount is one outstanding finding and what became of it. Class is
// copied from the finding record, which is the only source it has.
type findingAccount struct {
	ID       string           `json:"id"`
	Class    roles.Class      `json:"class"`
	Status   findingStatus    `json:"status"`
	Detail   string           `json:"detail"`
	Response *findingResponse `json:"response,omitempty"`
	Finding  roles.Finding    `json:"finding"`
}

// findingAccounting is one cycle's reconciliation of every outstanding finding.
type findingAccounting struct {
	Accounts []findingAccount `json:"accounts"`
	// Malformed is why the responder's accounting could not be read, if it
	// could not; every finding is then silent.
	Malformed string `json:"malformed,omitempty"`
	// Unmatched are response ids that name no outstanding finding. Reported,
	// and they discharge nothing.
	Unmatched []string `json:"unmatched,omitempty"`
}

// outstanding is the part of the open review a response cycle must account
// for: every finding, since none is discharged until a review resolves it.
// Severity decides what a finding does to the verdict, never whether it is owed
// an answer: a minor finding dropped here would leave the accounting solely
// because of its severity.
func (o openReview) outstanding() []roles.Finding {
	return append([]roles.Finding(nil), o.Findings...)
}

// minorFindings are the findings ReviewVerdict.Instruction leaves out of the
// revision instruction because they do not affect the decision. They are still
// outstanding, so the responder must be shown them to account for them.
func minorFindings(findings []roles.Finding) []roles.Finding {
	var out []roles.Finding
	for _, f := range findings {
		if f.Severity == roles.Minor {
			out = append(out, f)
		}
	}
	return out
}

// executedChecks names, by command line, every check the validation broker ran
// against this exact candidate. A check that was not permitted or could not
// start did not run, and a check bound to other bytes is about another
// candidate; neither is here.
func executedChecks(b validation.Bundle, candidateID, diffDigest string) map[string]bool {
	out := map[string]bool{}
	for _, c := range b.Checks {
		if !c.Certifies(candidateID, diffDigest) {
			continue
		}
		switch c.Outcome {
		case validation.Passed, validation.Failed, validation.Infrastructure:
			out[commandLine(c.Command, c.Args)] = true
		}
	}
	return out
}

func commandLine(command string, args []string) string {
	return strings.TrimSpace(strings.Join(append([]string{command}, args...), " "))
}

// reconcileFindings accounts for each outstanding finding by id. changed is the
// set of candidate files that moved since the findings were raised; executed
// is every check the broker ran against this candidate (see executedChecks).
func reconcileFindings(outstanding []roles.Finding, responses []findingResponse, malformed error, changed, executed map[string]bool) findingAccounting {
	var acct findingAccounting
	if malformed != nil {
		acct.Malformed = malformed.Error()
		responses = nil
	}
	byID := map[string][]findingResponse{}
	for _, r := range responses {
		byID[r.ID] = append(byID[r.ID], r)
	}
	open := map[string]bool{}
	for _, f := range outstanding {
		open[f.ID] = true
		a := findingAccount{ID: f.ID, Class: f.Class, Finding: f}
		rs := byID[f.ID]
		switch {
		case !f.Class.Valid():
			// An advisory verdict is not refused for this at validation; it is
			// refused here, before any response could be read as answering it.
			a.Status, a.Detail = findingUnclassified, fmt.Sprintf("the finding record carries class %q, so no response can discharge it; a finding's class is never inferred", f.Class)
		case len(rs) == 0:
			a.Status, a.Detail = findingSilent, "no response names it"
		case len(rs) > 1:
			a.Status, a.Detail = findingAmbiguous, fmt.Sprintf("%d responses name it, and a finding is answered once", len(rs))
		default:
			r := rs[0]
			a.Response = &r
			a.Status, a.Detail = judgeResponse(f, r, changed, executed)
		}
		acct.Accounts = append(acct.Accounts, a)
	}
	for _, r := range responses {
		if !open[r.ID] {
			acct.Unmatched = append(acct.Unmatched, r.ID)
		}
	}
	return acct
}

// judgeResponse checks one response against the class the FINDING carries. The
// most it can conclude is answered: see the note above findingResponseKind.
func judgeResponse(f roles.Finding, r findingResponse, changed, executed map[string]bool) (findingStatus, string) {
	if r.Response == respondDisagree {
		reading := strings.TrimSpace(r.ClaimedClass)
		if reading == "" {
			reading = "another class"
		}
		return findingDisputed, fmt.Sprintf("the responder reads this %s finding as %s (%s); a disagreement is escalated and does not discharge it",
			f.Class, reading, oneLine(r.Reason))
	}
	if string(r.Response) != string(f.Class) {
		return findingReclassified, fmt.Sprintf("the finding record says %s; a %q response does not discharge it, and the responder does not set a finding's class",
			f.Class, r.Response)
	}
	switch f.Class {
	case roles.ClassEvidence:
		if len(r.Executions) == 0 {
			return findingUnattributed, "an evidence finding is answered by a check the validation broker ran, and the response cites none; the responder's own account is not execution evidence"
		}
		var ran, fabricated []string
		for _, x := range r.Executions {
			if executed[strings.TrimSpace(x)] {
				ran = append(ran, strings.TrimSpace(x))
			} else {
				fabricated = append(fabricated, x)
			}
		}
		if len(fabricated) != 0 {
			return findingUnattributed, fmt.Sprintf("the response cites executions the validation broker did not run against this candidate (%s)",
				strings.Join(fabricated, ", "))
		}
		return findingAnswered, "answered with executions the validation broker ran (" + strings.Join(ran, ", ") + "); open until its reviewer resolves it"
	case roles.ClassCode, roles.ClassScope:
		var moved []string
		for _, p := range r.Paths {
			if changed[p] {
				moved = append(moved, p)
			}
		}
		if len(moved) == 0 {
			return findingUnattributed, fmt.Sprintf("a %s finding is discharged by a change to the candidate, and no file the response names (%s) changed since it was raised",
				f.Class, strings.Join(r.Paths, ", "))
		}
		return findingAnswered, "answered by a change to " + strings.Join(moved, ", ") + "; open until its reviewer resolves it"
	}
	// Unreachable: reconcileFindings refuses an unclassified finding first.
	return findingUnclassified, fmt.Sprintf("the finding carries class %q, which no response can discharge", f.Class)
}

// answered is every account whose response goes to the reviewer.
func (a findingAccounting) answered() []findingAccount { return a.with(findingAnswered) }

// disputed is every account the responder escalated as a class disagreement.
func (a findingAccounting) disputed() []findingAccount { return a.with(findingDisputed) }

// unanswered is every account left open by silence, a refused
// reclassification, an unattributable response or an ambiguous one.
func (a findingAccounting) unanswered() []findingAccount {
	var out []findingAccount
	for _, x := range a.Accounts {
		if x.Status != findingAnswered && x.Status != findingDisputed {
			out = append(out, x)
		}
	}
	return out
}

func (a findingAccounting) with(s findingStatus) []findingAccount {
	var out []findingAccount
	for _, x := range a.Accounts {
		if x.Status == s {
			out = append(out, x)
		}
	}
	return out
}

// diagnosis names every finding the response left unanswered, by id and class,
// and why.
func (a findingAccounting) diagnosis() string {
	var open []string
	for _, x := range a.Accounts {
		if x.Status == findingAnswered {
			continue
		}
		open = append(open, fmt.Sprintf("[%s] %s finding %s: %s", x.ID, x.Class, x.Status, x.Detail))
	}
	if len(open) == 0 {
		return fmt.Sprintf("all %d outstanding finding(s) are answered and go to their reviewer to be resolved", len(a.Accounts))
	}
	msg := fmt.Sprintf("%d of %d outstanding finding(s) are still open: %s", len(open), len(a.Accounts), strings.Join(open, "; "))
	if a.Malformed != "" {
		msg += " (the responder's accounting could not be read: " + a.Malformed + ")"
	}
	if len(a.Unmatched) != 0 {
		msg += " (responses named no outstanding finding: " + strings.Join(a.Unmatched, ", ") + ")"
	}
	return msg
}

// renderFindings lists findings as the implementer reads them.
func renderFindings(findings []roles.Finding) string {
	var b strings.Builder
	for _, f := range findings {
		b.WriteString("- " + f.Line() + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// findingResponseContract tells the responder how its answer is read.
func findingResponseContract(outstanding []roles.Finding) string {
	if len(outstanding) == 0 {
		return ""
	}
	ids := make([]string, 0, len(outstanding))
	for _, f := range outstanding {
		ids = append(ids, f.ID+" ("+string(f.Class)+")")
	}
	return `

EVERY FINDING ABOVE IS OPEN UNTIL YOU ACCOUNT FOR IT BY ID: ` + strings.Join(ids, ", ") + `
A finding's class is set by the reviewer and binds your response. A code finding is answered only by
a change to the candidate; an evidence finding only by checks the validation broker runs against the
candidate, cited by their exact command line in "executions" -- your own account of what you ran is not
execution evidence; a scope finding only by a change that brings the candidate back inside its bound.
An answer does not discharge a finding: the reviewer that raised it is asked, by id, whether the
candidate now answers it, and it stays open until that reviewer says so.
You do not set or change a finding's class. If you believe one is misclassified, answer it "disagree"
with claimed_class and reason: that is escalated to the architect and does NOT discharge the finding.
A finding you do not answer stays open, and the cycle does not converge however the diff moved.
End your output with exactly one line:
` + findingResponsesMarker + ` {"responses":[{"id":"f1","response":"code"|"evidence"|"scope"|"disagree","paths":["candidate files you changed for it"],"executions":["exact command line of a validation check"],"evidence":"what it observed","claimed_class":"only for disagree","reason":"why"}]}`
}

// classDisputePrompt puts a responder's class disagreement to the architect.
func classDisputePrompt(task, plan, audit string, o openReview, acct findingAccounting) string {
	var disputes strings.Builder
	for _, a := range acct.disputed() {
		disputes.WriteString("  " + a.Finding.Line() + "\n    responder: " + a.Detail + "\n")
	}
	return fmt.Sprintf(`The implementer disputes the class of one or more open review findings. The class belongs to the finding, which %s raised; the disagreement does not discharge it and it stays open. Decide how the plan proceeds under your architectural authority and return a revised bounded plan. Escalate to the human only if the decision changes human-owned intent/policy/contract/trust authority.

TASK:
%s

CURRENT PLAN:
%s

SENSEI AUDIT:
%s

DISPUTED FINDINGS:
%s
Return ONLY the same architecture JSON contract as before.`, o.Reviewer, task, plan, audit, disputes.String())
}

// diffFileDigests names each file section of a diff by the digest of its
// bytes, so two candidates can be compared file by file.
func diffFileDigests(diff string) map[string]string {
	out := map[string]string{}
	var section []string
	flush := func() {
		if len(section) == 0 {
			return
		}
		text := strings.Join(section, "\n")
		for _, f := range report.FromDiff(text).Files {
			sum := sha256.Sum256([]byte(text))
			out[f.Path] = hex.EncodeToString(sum[:])
		}
		section = nil
	}
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
		}
		section = append(section, line)
	}
	flush()
	return out
}

// changedSince is every file whose section differs between the candidate the
// findings were raised on and this one, including a file that entered or left
// the diff.
func changedSince(before map[string]string, diff string) map[string]bool {
	after := diffFileDigests(diff)
	changed := map[string]bool{}
	for p, d := range after {
		if before[p] != d {
			changed[p] = true
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			changed[p] = true
		}
	}
	return changed
}

// reviewerSettlement is what the reviewer's verdict made of the findings that
// went to it answered.
type reviewerSettlement struct {
	// Discharged are the answered findings the reviewer named resolved.
	Discharged []roles.Finding `json:"discharged,omitempty"`
	// Open are the answered findings it did not: re-raised under their own id,
	// or passed over in silence. Each keeps the class it was raised with.
	Open []roles.Finding `json:"open,omitempty"`
	// Unknown are resolved ids that name no answered finding. They discharge
	// nothing.
	Unknown []string `json:"unknown,omitempty"`
}

// settleByReviewer applies a verdict to the findings that went to it answered.
// A finding is discharged only when the verdict names its id in Resolved and
// does not also raise it again; the verdict's decision does not stand in for
// that, because an ACCEPT that never mentions a finding has not accounted for
// it, whatever it says about the candidate as a whole.
func settleByReviewer(answered []roles.Finding, v roles.ReviewVerdict) reviewerSettlement {
	resolved := map[string]bool{}
	for _, id := range v.Resolved {
		resolved[strings.TrimSpace(id)] = true
	}
	reraised := map[string]bool{}
	for _, f := range v.Findings {
		reraised[f.ID] = true
	}
	var s reviewerSettlement
	known := map[string]bool{}
	for _, f := range answered {
		known[f.ID] = true
		if resolved[f.ID] && !reraised[f.ID] {
			s.Discharged = append(s.Discharged, f)
			continue
		}
		s.Open = append(s.Open, f)
	}
	for _, id := range v.Resolved {
		if !known[strings.TrimSpace(id)] {
			s.Unknown = append(s.Unknown, id)
		}
	}
	return s
}

// diagnosis names every answered finding the reviewer left open.
func (s reviewerSettlement) diagnosis() string {
	open := make([]string, 0, len(s.Open))
	for _, f := range s.Open {
		open = append(open, fmt.Sprintf("[%s] %s finding", f.ID, f.Class))
	}
	msg := fmt.Sprintf("%d answered finding(s) discharged by their reviewer", len(s.Discharged))
	if len(open) != 0 {
		msg += fmt.Sprintf("; %d not resolved by it and still open: %s", len(open), strings.Join(open, ", "))
	}
	if len(s.Unknown) != 0 {
		msg += " (resolved ids naming no answered finding: " + strings.Join(s.Unknown, ", ") + ")"
	}
	return msg
}

// carriedWith is a new review's findings plus every answered finding the review
// neither resolved nor raised again: a finding does not close because the next
// review happened to be about something else.
func carriedWith(findings []roles.Finding, s reviewerSettlement) []roles.Finding {
	out := append([]roles.Finding(nil), findings...)
	present := map[string]bool{}
	for _, f := range findings {
		present[f.ID] = true
	}
	for _, f := range s.Open {
		if !present[f.ID] {
			out = append(out, f)
		}
	}
	return out
}

// answeredFindingsSection asks the reviewer to settle each answered finding by
// id. It carries the findings as they were raised and nothing the worker said
// about them.
func answeredFindingsSection(answered string) string {
	if strings.TrimSpace(answered) == "" {
		return ""
	}
	return `PREVIOUSLY RAISED FINDINGS, NOW ANSWERED BY THE WORKER:
` + answered + `
Each is open until you say otherwise, by id. For every one this candidate now answers -- a code or scope
finding by the change it names, an evidence finding by the validation evidence above -- add its id to
"resolved" in your JSON: {"resolved":["f1"]}. One that it does not answer, raise again under the same id.
An id you neither resolve nor raise again stays open, and an ACCEPT does not close it.

`
}

// outstandingFindings is the typed remainder of the task's open review.
func (e *Engine) outstandingFindings(taskID string) []roles.Finding {
	o, ok := e.openReview(taskID)
	if !ok {
		return nil
	}
	return o.outstanding()
}

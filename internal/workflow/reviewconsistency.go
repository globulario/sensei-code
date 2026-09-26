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
//
// THE LEDGER. A finding stays open, by id, until an independent review
// resolves that id in a way its class allows. It is not closed by the
// candidate moving: on the DF-19 resume a blocking code finding went
// unanswered while its evidence neighbour was answered, and one unrelated
// changed line would have let it through, because the rule then was that any
// change to the candidate or its evidence answered the whole review. The class
// is the one the reviewer recorded; nobody answering the finding can change it.

// OutstandingFinding is one finding held open by id: the reviewer's record of
// it, and the candidate it was raised against. The candidate matters because a
// code or scope finding can only be resolved on a candidate that has changed
// since it was raised.
type OutstandingFinding struct {
	Finding       roles.Finding `json:"finding"`
	RaisedBy      string        `json:"raised_by,omitempty"`
	RaisedDigest  string        `json:"raised_on_digest,omitempty"`
	RaisedOnTree  string        `json:"raised_on_tree,omitempty"`
	RaisedAttempt int           `json:"raised_on_attempt,omitempty"`
}

// openReview is the unanswered part of the review record, held by the engine
// per task: the latest non-accepting verdict, and the ledger of every finding
// no review has yet resolved.
type openReview struct {
	Reviewer         string          `json:"reviewer"`
	Attempt          int             `json:"review_attempt"`
	Decision         roles.Decision  `json:"decision"`
	Summary          string          `json:"summary"`
	Findings         []roles.Finding `json:"findings,omitempty"`
	CandidateDigest  string          `json:"candidate_digest"`
	CandidateTree    string          `json:"candidate_tree,omitempty"`
	EvidenceIdentity string          `json:"evidence_identity"`
	// Outstanding is the ledger, in the order the findings were raised.
	Outstanding []OutstandingFinding `json:"outstanding,omitempty"`
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

// openReviewFrom records a verdict left open, in the same identity language as
// everything else: the contradiction record names the content, not only the
// representation of it. It is the ledger's first entry for a task.
func openReviewFrom(v roles.ReviewVerdict, attempt int, candidateDigest, evidenceID string) openReview {
	next, _ := openReview{}.advance(v, attempt, candidateDigest, evidenceID)
	return next
}

// advance is the ledger after verdict v: v's resolutions applied to what was
// outstanding, then -- for a verdict that did not accept -- v's actionable
// findings added under their own ids. It returns the resolutions it refused to
// apply, stated, because a resolution that closed nothing must not read as one
// that did.
//
// An id already outstanding is never replaced: its class and the candidate it
// was raised against are the record until a review compatibly resolves it. The
// party answering a finding has no path into this function, and a later review
// that reuses the id has no path to rewrite it either.
func (o openReview) advance(v roles.ReviewVerdict, attempt int, candidateDigest, evidenceID string) (openReview, []string) {
	var refused []string
	resolved := map[string]bool{}
	for _, r := range v.Resolutions {
		if r.Outcome != roles.Resolved {
			continue
		}
		id := strings.TrimSpace(r.FindingID)
		for _, of := range o.Outstanding {
			if of.Finding.ID != id {
				continue
			}
			if of.Finding.Class.RequiresCandidateChange() && !of.candidateMoved(v.Provenance.CandidateTree, candidateDigest) {
				refused = append(refused, fmt.Sprintf("%s was resolved on the candidate it was raised against; a %s finding is discharged only by a changed candidate, so it stays open", describeID(of.Finding), of.Finding.Class))
				continue
			}
			resolved[id] = true
		}
	}
	next := openReview{
		Reviewer:         o.Reviewer,
		Attempt:          o.Attempt,
		Decision:         o.Decision,
		Summary:          o.Summary,
		Findings:         o.Findings,
		CandidateDigest:  o.CandidateDigest,
		CandidateTree:    o.CandidateTree,
		EvidenceIdentity: o.EvidenceIdentity,
	}
	for _, of := range o.Outstanding {
		if !resolved[of.Finding.ID] {
			next.Outstanding = append(next.Outstanding, of)
		}
	}
	if v.Accepts() {
		return next, refused
	}
	next.Reviewer = v.Provenance.Provider
	next.Attempt = attempt
	next.Decision = v.Decision
	next.Summary = v.Summary
	next.Findings = v.Findings
	next.CandidateDigest = candidateDigest
	next.CandidateTree = v.Provenance.CandidateTree
	next.EvidenceIdentity = evidenceID
	for _, f := range v.Findings {
		if f.Severity == roles.Minor {
			// A minor finding does not affect the decision, so it is not owed an
			// answer either.
			continue
		}
		if held, ok := next.held(f.ID); ok {
			if held.Class == f.Class && strings.TrimSpace(held.Claim) == strings.TrimSpace(f.Claim) {
				// The same finding restated: the record it was first raised
				// with stands, raised-against identity and all.
				continue
			}
			// An outstanding id is never rewritten. A later finding under it --
			// a new objection, or the old one in another class -- would
			// otherwise replace the reviewer-owned record without resolving it,
			// so it is held under an id of its own and the original stays open.
			reused := f.ID
			f.ID = next.freshID(f.ID, attempt)
			refused = append(refused, fmt.Sprintf("a later finding reused the outstanding id %s; %s stays open as recorded and the later finding is held as %s (%s, %s)",
				reused, describeID(held), f.ID, f.Class, f.Severity))
		}
		next.Outstanding = append(next.Outstanding, OutstandingFinding{Finding: f, RaisedBy: v.Provenance.Provider,
			RaisedDigest: candidateDigest, RaisedOnTree: v.Provenance.CandidateTree, RaisedAttempt: attempt})
	}
	return next, refused
}

// held is the outstanding finding recorded under id, if any.
func (o openReview) held(id string) (roles.Finding, bool) {
	for _, of := range o.Outstanding {
		if of.Finding.ID == id {
			return of.Finding, true
		}
	}
	return roles.Finding{}, false
}

// freshID is an id no outstanding finding holds, derived from a reused one and
// the review attempt that reused it.
func (o openReview) freshID(id string, attempt int) string {
	candidate := fmt.Sprintf("%s-r%d", id, attempt)
	for n := 2; ; n++ {
		if _, taken := o.held(candidate); !taken {
			return candidate
		}
		candidate = fmt.Sprintf("%s-r%d.%d", id, attempt, n)
	}
}

// candidateMoved reports whether the candidate named by tree and digest is a
// different candidate from the one this finding was raised against. The
// content identity decides when both sides have one; otherwise the digest does.
// A candidate that cannot be shown to differ has not moved: silence on identity
// is not change.
func (of OutstandingFinding) candidateMoved(tree, digest string) bool {
	if of.RaisedOnTree != "" && tree != "" {
		return of.RaisedOnTree != tree
	}
	if of.RaisedDigest != "" && digest != "" {
		return of.RaisedDigest != digest
	}
	return false
}

// findings is the ledger as the findings themselves, in the order raised.
func (o openReview) findings() []roles.Finding {
	out := make([]roles.Finding, 0, len(o.Outstanding))
	for _, of := range o.Outstanding {
		out = append(out, of.Finding)
	}
	return out
}

// rendered is the ledger as the reviewer's packet carries it: one line per
// finding, with its id and recorded class.
func (o openReview) rendered() string {
	var b strings.Builder
	for _, of := range o.Outstanding {
		b.WriteString("- " + of.Finding.Line() + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// outstandingIDs names what the ledger holds, each with its recorded class.
func (o openReview) outstandingIDs() string {
	names := make([]string, 0, len(o.Outstanding))
	for _, of := range o.Outstanding {
		names = append(names, describeID(of.Finding))
	}
	return strings.Join(names, ", ")
}

func describeID(f roles.Finding) string {
	return fmt.Sprintf("%s (%s, %s)", f.ID, f.Class, f.Severity)
}

// contradicts reports whether an accepting verdict on this candidate and
// evidence contradicts the open review rather than answering it.
//
// An accept contradicts the review while any finding it holds is still
// outstanding once the accept's own resolutions are applied. The candidate or
// the evidence changing does not answer a finding: a finding is answered by an
// independent review resolving its id, compatibly with its class, and by
// nothing else.
//
// A non-accepting verdict that raised no actionable finding has no id to be
// answered by, and for that one case the identity rule stands: an ACCEPT on
// the same bytes (content AND representation) and the same outcomes flips on
// nothing, and is a contradiction.
func (o openReview) contradicts(accepting roles.ReviewVerdict, candidateDigest, evidenceID string) bool {
	if !accepting.Accepts() {
		return false
	}
	if len(o.Outstanding) != 0 {
		next, _ := o.advance(accepting, o.Attempt+1, candidateDigest, evidenceID)
		return len(next.Outstanding) != 0
	}
	return o.CandidateDigest != "" && o.CandidateDigest == candidateDigest &&
		o.EvidenceIdentity != "" && o.EvidenceIdentity == evidenceID &&
		o.CandidateTree == accepting.Provenance.CandidateTree
}

// describe is the contradiction stated once, for the event and the receipt.
func (o openReview) describe(accepting roles.ReviewVerdict) string {
	if len(o.Outstanding) != 0 {
		return fmt.Sprintf("review contradiction: %s (attempt %d) accepted candidate %s while finding(s) earlier reviews raised remain unresolved: %s. An accept that does not resolve an outstanding finding by id does not answer it. Open: %s",
			accepting.Provenance.Provider, o.Attempt+1, shortDigest(accepting.Provenance.CandidateDigest), o.outstandingIDs(), oneLine(o.Summary))
	}
	return fmt.Sprintf("review contradiction on an unchanged candidate: %s (attempt %d) did not accept and %s (attempt %d) accepted the same candidate digest %s on the same evidence; nothing changed but the reviewer. Open: %s",
		o.Reviewer, o.Attempt, accepting.Provenance.Provider, o.Attempt+1, shortDigest(o.CandidateDigest), oneLine(o.Summary))
}

// contradictionPrompt puts the two verdicts to the architect for adjudication.
func contradictionPrompt(task, plan, audit string, o openReview, accepting roles.ReviewVerdict) string {
	var findings strings.Builder
	for _, f := range o.Findings {
		findings.WriteString("  " + f.Line() + "\n")
	}
	situation := "Two independent reviews of the SAME candidate revision, on the SAME executed evidence, disagree. Nothing about the candidate changed between them; only the reviewer did."
	if len(o.Outstanding) != 0 {
		findings.Reset()
		for _, of := range o.Outstanding {
			findings.WriteString("  " + of.Finding.Line() + "\n")
		}
		situation = "An independent review accepted the candidate while findings earlier reviews raised remain unresolved by id. A finding is answered only by a review resolving it compatibly with its class, never by the candidate merely changing."
	}
	return fmt.Sprintf(`%s Adjudicate using your architectural authority: decide whether the earlier findings stand, and issue a revised bounded plan that either requires them to be answered or records why they do not apply. Escalate to the human only if the decision changes human-owned intent/policy/contract/trust authority.

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

Return ONLY the same architecture JSON contract as before, with one additional field: "adjudication": "revise" if the earlier finding applies and the revised plan says what is still owed, or "adjudication": "accepting_review_stands" if the earlier finding does not apply to this candidate and no edit is owed.`, situation, task, plan, audit, o.Reviewer, o.Summary, findings.String(), accepting.Provenance.Provider, accepting.Summary)
}

// adjudicated is the ledger after the architect rules on a contradiction, and
// whether the accepting review may go on to acceptance.
//
// The architect owns the plan, not the review record. A ruling that the
// accepting review stands resolves no id: while any finding is outstanding the
// ledger is held unchanged and there is no acceptance to take, until an
// independent review resolves each id compatibly. Only a contradiction with no
// id to answer -- the identity rule -- is settled by the ruling itself.
func (o openReview) adjudicated(stands bool) (hold openReview, proceed bool) {
	if !stands || len(o.Outstanding) != 0 {
		return o, false
	}
	return openReview{}, true
}

// FindingResponsesHeading introduces an implementer's per-finding responses.
// The block after it is a JSON array of roles.FindingResponse.
const FindingResponsesHeading = "FINDING RESPONSES:"

// parseFindingResponses reads the implementer's per-finding responses from its
// report: the JSON array after the LAST FindingResponsesHeading. No heading is
// no responses, which accounts for nothing; a heading whose block cannot be
// read is an error, and also accounts for nothing.
func parseFindingResponses(report string) ([]roles.FindingResponse, error) {
	at := strings.LastIndex(report, FindingResponsesHeading)
	if at < 0 {
		return nil, nil
	}
	rest := report[at+len(FindingResponsesHeading):]
	open := strings.Index(rest, "[")
	if open < 0 {
		return nil, errors.New("the FINDING RESPONSES block holds no JSON array")
	}
	var out []roles.FindingResponse
	if err := json.NewDecoder(strings.NewReader(rest[open:])).Decode(&out); err != nil {
		return nil, fmt.Errorf("the FINDING RESPONSES block could not be read: %w", err)
	}
	return out, nil
}

// findingDisagreement is an implementer that believes an outstanding finding
// is wrong or misclassified. It is routed to the architect and discharges
// nothing.
type findingDisagreement struct {
	Finding  roles.Finding         `json:"finding"`
	Response roles.FindingResponse `json:"response"`
}

// findingAccount is one implementer cycle measured against the ledger, id by
// id. It decides nothing about acceptance -- only an independent review
// resolves a finding -- but it decides whether the cycle may go to review at
// all: a cycle that answers some findings and is silent on others has not
// converged, however the candidate moved.
type findingAccount struct {
	// Unanswered states, per id, why the cycle did not account for it.
	Unanswered []string `json:"unanswered,omitempty"`
	// Disagreements are owed the architect.
	Disagreements []findingDisagreement `json:"disagreements,omitempty"`
	// Answered are the ids answered in the kind their class requires. They
	// stay outstanding until a review resolves them.
	Answered []string `json:"answered,omitempty"`
	// EvidenceOnly: every outstanding finding was answered and every one is an
	// evidence finding, so no change to the candidate was owed.
	EvidenceOnly bool `json:"evidence_only"`
}

// accounted reports whether every outstanding finding was answered.
func (a findingAccount) accounted() bool {
	return len(a.Unanswered) == 0 && len(a.Disagreements) == 0
}

// describe is the accounting as the operator, the next cycle and the terminal
// read it. Every id that is not answered is named.
func (a findingAccount) describe() string {
	var parts []string
	for _, u := range a.Unanswered {
		parts = append(parts, u)
	}
	for _, d := range a.Disagreements {
		parts = append(parts, fmt.Sprintf("finding %s: the implementer disagrees with it (it answered as %q); a disagreement is routed to the architect and discharges nothing", describeID(d.Finding), d.Response.Class))
	}
	if len(parts) == 0 {
		return "every outstanding finding was answered: " + strings.Join(a.Answered, ", ") + "; each stays open until an independent review resolves it"
	}
	return "the outstanding findings are not all accounted for: " + strings.Join(parts, "; ")
}

// account measures the implementer's responses against the ledger.
//
// The class compared is the class on the finding's record. A response carries
// the class its author believes it is answering; when that differs, the
// response discharges nothing, and the only honest route for a responder that
// thinks the class is wrong is to answer "disagreed". A code or scope finding
// answered on a candidate that has not moved since it was raised is not
// answered: only a change can discharge it.
func (o openReview) account(responses []roles.FindingResponse, parseErr error, candidateTree, candidateDigest string) findingAccount {
	var a findingAccount
	byID := map[string][]roles.FindingResponse{}
	for _, r := range responses {
		id := strings.TrimSpace(r.FindingID)
		byID[id] = append(byID[id], r)
	}
	unreadable := ""
	if parseErr != nil {
		unreadable = " (" + parseErr.Error() + ")"
	}
	evidenceOnly := len(o.Outstanding) != 0
	for _, of := range o.Outstanding {
		f := of.Finding
		if f.Class != roles.ClassEvidence {
			evidenceOnly = false
		}
		got := byID[f.ID]
		switch {
		case len(got) == 0:
			a.Unanswered = append(a.Unanswered, fmt.Sprintf("finding %s was not answered%s", describeID(f), unreadable))
			continue
		case len(got) > 1:
			a.Unanswered = append(a.Unanswered, fmt.Sprintf("finding %s was answered %d times, so no one answer is the implementer's", describeID(f), len(got)))
			continue
		}
		r := got[0]
		switch {
		case r.Disposition == roles.Disagreed:
			a.Disagreements = append(a.Disagreements, findingDisagreement{Finding: f, Response: r})
		case r.Disposition != roles.Discharged:
			a.Unanswered = append(a.Unanswered, fmt.Sprintf("finding %s was answered with disposition %q, which is neither %q nor %q", describeID(f), r.Disposition, roles.Discharged, roles.Disagreed))
		case r.Class != f.Class:
			a.Unanswered = append(a.Unanswered, fmt.Sprintf("finding %s is a %s finding on the review record, and a response answering it as %q does not discharge it: the class is the finding's, not the response's; answer %q to dispute it", describeID(f), f.Class, r.Class, roles.Disagreed))
		case f.Class.RequiresCandidateChange() && !of.candidateMoved(candidateTree, candidateDigest):
			a.Unanswered = append(a.Unanswered, fmt.Sprintf("finding %s is a %s finding and the candidate has not changed since it was raised; only a change to the candidate can discharge it", describeID(f), f.Class))
		default:
			a.Answered = append(a.Answered, f.ID)
		}
	}
	a.EvidenceOnly = evidenceOnly && a.accounted()
	return a
}

// responseInstructions tells the implementer which findings it owes an answer
// and the exact form of the answer. It lists each finding with the class on
// its record, because that is the class the answer is checked against.
func (o openReview) responseInstructions() string {
	if len(o.Outstanding) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("OUTSTANDING FINDINGS -- each must be answered by id, in the kind its class requires:\n")
	for _, of := range o.Outstanding {
		b.WriteString("- " + of.Finding.Line() + "\n")
	}
	b.WriteString(`
A code or scope finding is discharged only by changing the candidate; an evidence finding is
discharged by evidence and needs no change to the candidate. The class on each finding is its
reviewer's and is not yours to change. If you believe a finding is wrong or misclassified, answer
it "disagreed": that goes to the architect, and the finding stays open. A finding you do not answer
stays open, and a cycle that leaves one unanswered does not go to review. End your output with:

` + FindingResponsesHeading + `
[{"finding_id":"<id>","class":"code"|"evidence"|"scope","disposition":"discharged"|"disagreed","account":"what you did, or why you disagree"}]`)
	return b.String()
}

// disagreementPrompt puts an implementer's disagreement with outstanding
// findings to the architect. The implementer's account is carried here, to the
// party that can change the plan; it is never carried to the reviewer.
func disagreementPrompt(task, plan string, o openReview, ds []findingDisagreement) string {
	var b strings.Builder
	for _, d := range ds {
		b.WriteString("  " + d.Finding.Line() + "\n")
		b.WriteString(fmt.Sprintf("    the implementer answered it as %q and disagrees: %s\n", d.Response.Class, oneLine(d.Response.Account)))
	}
	return fmt.Sprintf(`The implementer disagrees with outstanding review findings, including possibly the class the reviewer gave them. A disagreement is routed to you and does not discharge the finding: each one stays open until an independent review resolves it, whatever you decide. Decide with your architectural authority whether the plan must change so the finding can be answered, or whether it stands as written. Escalate to the human only if the decision changes human-owned intent/policy/contract/trust authority.

TASK:
%s

CURRENT PLAN:
%s

DISPUTED FINDINGS (class as the reviewer recorded it):
%s
ALL OUTSTANDING FINDINGS: %s

Return ONLY the same architecture JSON contract as before.`, task, plan, b.String(), o.outstandingIDs())
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

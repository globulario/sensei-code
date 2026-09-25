package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// PER-FINDING ACCOUNTING: THE CLASS OF A FINDING BINDS THE CLASS OF ITS RESPONSE.
//
// Observed 2026-09-25 on the DF-19 resume. One independent review returned two
// findings of different kinds: a BLOCKING one about a code path that reported a
// refusal as status and continued, and a MAJOR one about execution evidence that
// was never retained. The implementer answered the evidence finding fully and
// closed the cycle in its own words -- "the review finding was evidence-only, so
// no code changed" -- singular. The blocking code defect had been absorbed into
// the evidence-only reading of its neighbour and was never addressed.
//
// The engine did refuse that cycle, but on a PROXY: the candidate was
// byte-identical to the reviewed one, and the identical-diff check fired. Had the
// implementer touched one unrelated line, the diff would have differed, the proxy
// would have passed, and a blocking code defect would have been carried forward
// as answered.
//
// So convergence is decided here instead: every finding the cycle was asked to
// answer is accounted for BY ID, with an answer of the FINDING'S OWN class. The
// identical-diff check stays where it is, as a cheap backstop that catches a
// worker repeating itself, and it decides nothing.
//
// What this predicate does NOT claim: that a repair is sufficient. It establishes
// that an answer of the right KIND exists for each finding and that the answer
// has something behind it. Whether the code change actually fixes the defect is
// the reviewer's judgement, which is why the review still runs after this passes.

// findingResponseHeading introduces the implementer's per-finding accounting.
//
// A heading rather than "the whole reply is JSON": an implementation turn's
// reply is prose written for a person, and the accounting is a payload inside
// it, in the same relationship a review payload has to a review artifact.
const findingResponseHeading = "FINDING RESPONSE (JSON):"

// dischargeKind is what an implementer offers in answer to one finding.
//
// The vocabulary mirrors roles.FindingClass deliberately: a response discharges
// a finding when it is of the finding's own kind, so compatibility is decided by
// membership rather than by a policy table somebody can widen one exception at a
// time.
type dischargeKind string

const (
	// dischargeCode is a change to the candidate.
	dischargeCode dischargeKind = "code_change"
	// dischargeEvidence is retained execution evidence.
	dischargeEvidence dischargeKind = "evidence"
	// dischargeScope is a correction that brings the change back inside its bound.
	dischargeScope dischargeKind = "scope_correction"
	// dischargeDisagreement is the implementer saying the finding is misclassified
	// or does not apply. It ROUTES to the architect and discharges nothing.
	dischargeDisagreement dischargeKind = "disagreement"
)

// dischargedClass is the one finding class this kind of answer can discharge.
// An unrecognised kind discharges nothing, which is why the zero value is "".
func (d dischargeKind) dischargedClass() roles.FindingClass {
	switch d {
	case dischargeCode:
		return roles.ClassCode
	case dischargeEvidence:
		return roles.ClassEvidence
	case dischargeScope:
		return roles.ClassScope
	}
	return ""
}

// findingResponse is an implementer's account of ONE finding, named by its id.
type findingResponse struct {
	ID        string        `json:"id"`
	Discharge dischargeKind `json:"discharge"`
	// ChangedFiles are the candidate paths the response says carry the repair.
	// They are checked against the paths the candidate actually changed, so a
	// claimed code change that is not in the candidate is not a code change.
	ChangedFiles []string `json:"changed_files,omitempty"`
	// Evidence is the retained execution evidence an evidence finding is
	// answered with: the command, where its output is kept, what it shows.
	Evidence string `json:"evidence,omitempty"`
	// Disagreement is why the implementer believes the finding is misclassified
	// or does not apply. It is a route to the architect, never a discharge.
	Disagreement string `json:"disagreement,omitempty"`
	// ClaimedClass is the implementer's own reading of the finding's class. It is
	// INPUT: it is recorded so a disagreement can be stated precisely, and it is
	// never read as the class. The class comes from the finding record.
	ClaimedClass roles.FindingClass `json:"claimed_class,omitempty"`
}

// findingStatus is what became of one outstanding finding in one cycle.
type findingStatus string

const (
	// findingDischarged: answered, in the finding's own class, with something
	// behind the answer.
	findingDischarged findingStatus = "discharged"
	// findingUnanswered: no response named this id. This is the measured defect.
	findingUnanswered findingStatus = "unanswered"
	// findingWrongClass: answered with a kind of work that cannot discharge this
	// kind of finding.
	findingWrongClass findingStatus = "wrong_class"
	// findingUnsubstantiated: the right kind of answer with nothing behind it.
	findingUnsubstantiated findingStatus = "unsubstantiated"
	// findingDisputed: the implementer disagrees. Routed, still open.
	findingDisputed findingStatus = "disputed"
	// findingUnusable: the finding itself cannot be accounted for -- it names no
	// id to answer, or no class to answer in. Refused rather than guessed, and
	// routed to the party that can re-state it.
	findingUnusable findingStatus = "unusable"
)

// findingAccount is one finding and what became of it.
type findingAccount struct {
	Finding roles.Finding      `json:"finding"`
	Status  findingStatus      `json:"status"`
	Detail  string             `json:"detail"`
	Claimed roles.FindingClass `json:"claimed_class,omitempty"`
}

func (a findingAccount) discharged() bool { return a.Status == findingDischarged }

// name is how the diagnosis refers to a finding: its id, which is what the
// accounting is keyed by. A finding with no id is named by its own line, so the
// refusal still says which objection it is about.
func (a findingAccount) name() string {
	if id := strings.TrimSpace(a.Finding.ID); id != "" {
		return id
	}
	return oneLine(a.Finding.Line())
}

// findingAccounting is the cycle's account of every finding it was asked to
// answer. It is the convergence predicate.
type findingAccounting struct {
	Accounts []findingAccount `json:"accounts"`
}

// converged reports whether every outstanding finding was discharged
// compatibly. Silence about a finding is not convergence, and neither is a
// moved diff: nothing about the candidate's bytes appears in this answer except
// as the substantiation of a claimed code change.
func (acc findingAccounting) converged() bool {
	for _, a := range acc.Accounts {
		if !a.discharged() {
			return false
		}
	}
	return true
}

// open returns the accounts that are not discharged.
func (acc findingAccounting) open() []findingAccount {
	var out []findingAccount
	for _, a := range acc.Accounts {
		if !a.discharged() {
			out = append(out, a)
		}
	}
	return out
}

// openFindings returns the findings that are still owed an answer, so a later
// cycle carries them rather than losing them to the next review's silence.
func (acc findingAccounting) openFindings() []roles.Finding {
	var out []roles.Finding
	for _, a := range acc.open() {
		out = append(out, a.Finding)
	}
	return out
}

// routed reports whether something here is the architect's to settle rather than
// the implementer's: a disputed class, or a finding that cannot be accounted for
// as written. Neither is a discharge, and neither may be dropped.
func (acc findingAccounting) routed() bool {
	for _, a := range acc.open() {
		if a.Status == findingDisputed || a.Status == findingUnusable {
			return true
		}
	}
	return false
}

// diagnose names every finding that is still open, BY ID, and why. The id is
// the point: "the candidate did not change" is a statement about bytes, and the
// reader needs to know which objection went unanswered.
func (acc findingAccounting) diagnose() string {
	open := acc.open()
	if len(open) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d of %d outstanding finding(s) were not discharged; a finding is discharged only by an answer of its own class:",
		len(open), len(acc.Accounts))
	for _, a := range open {
		b.WriteString("\n  [" + a.name() + "] " + string(a.Finding.Class) + " finding, " + string(a.Status) + ": " + a.Detail)
	}
	return b.String()
}

// accountFindings is the per-finding accounting: the findings this cycle was
// asked to answer, each matched to the response that named it, each judged
// against the class the FINDING carries.
//
// Minor findings are left out. They are the ones ReviewVerdict.Instruction does
// not carry, so the implementer was never asked for them, and demanding an
// account of something nobody asked for is a trap rather than a gate. That is
// severity deciding what was ASKED, which is what severity already means here;
// it never decides a finding's class.
func accountFindings(o openReview, responses []findingResponse, changedPaths []string) findingAccounting {
	byID := make(map[string]findingResponse, len(responses))
	for _, r := range responses {
		if id := strings.TrimSpace(r.ID); id != "" {
			byID[id] = r
		}
	}
	changed := make(map[string]bool, len(changedPaths))
	for _, p := range changedPaths {
		if p = strings.TrimSpace(p); p != "" {
			changed[p] = true
		}
	}
	var acc findingAccounting
	for _, f := range o.Findings {
		if f.Severity == roles.Minor {
			continue
		}
		r, answered := byID[strings.TrimSpace(f.ID)]
		acc.Accounts = append(acc.Accounts, accountOne(f, r, answered, changed))
	}
	return acc
}

// accountOne judges one response against one finding.
func accountOne(f roles.Finding, r findingResponse, answered bool, changed map[string]bool) findingAccount {
	account := findingAccount{Finding: f, Claimed: r.ClaimedClass}
	set := func(s findingStatus, detail string) findingAccount {
		account.Status, account.Detail = s, detail
		return account
	}
	if strings.TrimSpace(f.ID) == "" {
		return set(findingUnusable, "the finding names no id, so no response can account for it by id; the reviewer must re-state it")
	}
	if !f.Class.Valid() {
		// Refused, not guessed. Severity, wording and position are not evidence
		// of class, and a finding sorted into the nearest class would be
		// dischargeable by whatever that sorting happened to pick.
		return set(findingUnusable, fmt.Sprintf("the finding states class %q, which names no kind of answer; its class is not inferred from its severity or its wording", f.Class))
	}
	if !answered {
		return set(findingUnanswered, "no response in this cycle names this finding, and silence about a finding is not an answer to it")
	}
	if d := strings.TrimSpace(r.Disagreement); d != "" || r.Discharge == dischargeDisagreement {
		if d == "" {
			d = "(the response disagreed without saying why)"
		}
		return set(findingDisputed, "the implementer disputes this finding rather than discharging it, which routes it to the architect and leaves it open: "+oneLine(d))
	}
	if got := r.Discharge.dischargedClass(); got != f.Class {
		claimed := ""
		if r.ClaimedClass != "" && r.ClaimedClass != f.Class {
			claimed = fmt.Sprintf(" The response reads it as %q; the class comes from the finding, not from the party answering it.", r.ClaimedClass)
		}
		return set(findingWrongClass, fmt.Sprintf("the finding is %s and the response offers %q, which discharges %s work.%s",
			f.Class, r.Discharge, dischargeOrNothing(got), claimed))
	}
	switch f.Class {
	case roles.ClassCode, roles.ClassScope:
		named := false
		for _, p := range r.ChangedFiles {
			if changed[strings.TrimSpace(p)] {
				named = true
				break
			}
		}
		if !named {
			return set(findingUnsubstantiated, "the response claims a change to the candidate and names no path the candidate actually changed")
		}
	case roles.ClassEvidence:
		if strings.TrimSpace(r.Evidence) == "" {
			return set(findingUnsubstantiated, "the response claims retained evidence and names none")
		}
	}
	return set(findingDischarged, "answered in its own class")
}

// dischargeOrNothing renders what a discharge kind can settle, for a diagnosis
// that has to explain a mismatch without asserting a class the kind does not have.
func dischargeOrNothing(c roles.FindingClass) string {
	if c == "" {
		return "no recognised kind of"
	}
	return string(c)
}

// carryForwardOpenFindings keeps a finding that was never discharged, even when
// the next review does not repeat it.
//
// Without this the accounting is evadable by waiting: the next reviewer is a
// fresh session that may object to something else entirely, its verdict replaces
// the open one, and the finding nobody answered is gone. A finding leaves the
// record by being discharged, withdrawn by the architect, or re-stated -- never
// by a later review being silent about it.
func carryForwardOpenFindings(next, stillOpen []roles.Finding) []roles.Finding {
	restated := make(map[string]bool, len(next))
	for _, f := range next {
		if id := strings.TrimSpace(f.ID); id != "" {
			restated[id] = true
		}
	}
	out := append([]roles.Finding(nil), next...)
	for _, f := range stillOpen {
		id := strings.TrimSpace(f.ID)
		if id == "" || restated[id] {
			continue
		}
		out = append(out, f)
	}
	return out
}

// parseFindingResponses reads the accounting block out of an implementer's reply.
//
// ABSENT and MALFORMED are distinguished. Both discharge nothing -- the findings
// stay open either way -- and a reader of the refusal is entitled to know which
// happened, because one is a worker that did not account and the other is a
// worker that tried and produced something unreadable.
func parseFindingResponses(report string) ([]findingResponse, error) {
	i := strings.Index(report, findingResponseHeading)
	if i < 0 {
		return nil, nil
	}
	rest := report[i+len(findingResponseHeading):]
	object, ok := firstJSONObject(rest)
	if !ok {
		return nil, fmt.Errorf("the %s block is not a complete JSON object", findingResponseHeading)
	}
	var block struct {
		Responses []findingResponse `json:"responses"`
	}
	if err := json.Unmarshal([]byte(object), &block); err != nil {
		return nil, fmt.Errorf("the %s block does not decode: %w", findingResponseHeading, err)
	}
	return block.Responses, nil
}

// firstJSONObject returns the first brace-balanced object in s.
//
// Brace matching rather than "first { to last }": an implementation turn's reply
// is prose, and prose after the block that happens to contain a brace would make
// the whole reply unparseable under the looser rule.
func firstJSONObject(s string) (string, bool) {
	start := strings.Index(s, "{")
	if start < 0 {
		return "", false
	}
	depth, inString, escaped := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case escaped:
			escaped = false
		case inString && c == '\\':
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

// findingResponseContract is what the next implementer cycle is told it owes:
// an account of every outstanding finding, by id, in the finding's own class.
//
// The classes are rendered from the finding record, so the party answering reads
// the class rather than deciding it.
func findingResponseContract(o openReview) string {
	var owed []roles.Finding
	for _, f := range o.Findings {
		if f.Severity != roles.Minor {
			owed = append(owed, f)
		}
	}
	if len(owed) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nEVERY OUTSTANDING FINDING MUST BE ACCOUNTED FOR, BY ID.\n")
	b.WriteString("A finding says what KIND of thing is wrong, and only the same kind of thing can make it right:\n")
	b.WriteString("  class=code     -> discharge=\"code_change\", naming the candidate paths that carry the repair\n")
	b.WriteString("  class=evidence -> discharge=\"evidence\", naming the command run and where its output is retained\n")
	b.WriteString("  class=scope    -> discharge=\"scope_correction\", naming the candidate paths brought back inside the bound\n")
	b.WriteString("The class comes from the finding. Your own reading of it is input, never authority: if you believe a\n")
	b.WriteString("finding is misclassified or does not apply, say so as discharge=\"disagreement\" with a reason. That\n")
	b.WriteString("routes it to the architect and does NOT discharge it. Silence about a finding does not discharge it\n")
	b.WriteString("either, and neither does changing the candidate somewhere else.\n\n")
	b.WriteString("OUTSTANDING:\n")
	for _, f := range owed {
		b.WriteString("  " + f.Line() + "\n")
	}
	b.WriteString("\nEnd your reply with:\n")
	b.WriteString(findingResponseHeading + "\n")
	b.WriteString(`{"responses":[{"id":"<finding id>","discharge":"code_change"|"evidence"|"scope_correction"|"disagreement",` + "\n")
	b.WriteString(`  "changed_files":["path"],"evidence":"command and where its output is retained","disagreement":"why, if you disagree"}]}`)
	return b.String()
}

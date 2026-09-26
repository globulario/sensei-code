package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/runreceipt"
	"github.com/globulario/sensei-code/internal/validation"
)

// NON-CONVERGENCE IS OWED AN ARCHITECT RE-PLAN, NOT A FINAL FAILURE.
//
// DF-6, observed 2026-09-19 on task-1789775984590842441: Claude and then Codex
// each spent every review cycle, every cycle validated green, every review was a
// bounded REVISE with real findings -- and the task ended WorkflowFailed, which
// FindInterrupted reads as final. In the same instant the candidate record said
// disposition "resumable": 45 KB of reviewed work that "resumable state
// references". `resume --task` answered "no interrupted task with that id". Two
// records disagreed about whether the task was alive, and the resumable one was
// unreachable.
//
// The owner's ruling: exhausting every implementer's review cycles is its own
// terminal. The task stays itself, the candidate stays as it stands, and what it
// owes is an ARCHITECT re-plan over the candidate and its open findings -- the
// party that can change the plan, rather than another implementer re-running the
// same plan against the same objection. Resuming delivers exactly that, and
// records the re-plan as the task's plan (PlanProposed), which discharges the
// obligation and binds every later resume to the revised plan.
//
// Only exhaustion qualifies. A worker that errored, a structural refusal, a
// refuted prospective grant, or a failure beside exhaustion stay FAILED: they are
// not "the reviewer still objects", and calling them re-plannable would let an
// arbitrary failure buy another round.

// errReviewCyclesExhausted marks the one worker failure that means "every cycle
// was spent and the reviewer still asks for revision".
var errReviewCyclesExhausted = errors.New("candidate did not converge")

// OwedArchitectReplan is the only thing a non-converged task owes.
const OwedArchitectReplan = "architect_replan"

// NotConverged is the durable record of a WorkflowNotConverged terminal,
// carried back byte for byte by FindInterrupted.
type NotConverged struct {
	TaskID string `json:"task_id"`
	// Implementers are the workers that each exhausted their cycles, in order.
	Implementers []string `json:"implementers"`
	// ReviewCycles is the per-implementer budget each one spent.
	ReviewCycles int `json:"review_cycles"`
	// Owed is what a resume delivers: always OwedArchitectReplan.
	Owed string `json:"owed"`
}

// Describe is the one-line account the receipt and the terminal carry.
func (n NotConverged) Describe() string {
	return fmt.Sprintf("%s each spent %d review cycles and the reviewer still requires revision; owed: %s",
		strings.Join(n.Implementers, ", "), n.ReviewCycles, n.Owed)
}

// ParseNotConverged reads a recorded non-convergence back, refusing one that
// cannot say which task it belongs to or what it owes.
func ParseNotConverged(raw json.RawMessage) (NotConverged, error) {
	var n NotConverged
	if err := json.Unmarshal(raw, &n); err != nil {
		return NotConverged{}, fmt.Errorf("the non-convergence record is unreadable: %w", err)
	}
	if strings.TrimSpace(n.TaskID) == "" {
		return NotConverged{}, errors.New("the non-convergence record names no task")
	}
	if len(n.Implementers) == 0 || n.ReviewCycles <= 0 {
		return NotConverged{}, errors.New("the non-convergence record does not say who spent which review budget")
	}
	if n.Owed != OwedArchitectReplan {
		return NotConverged{}, fmt.Errorf("the non-convergence record owes %q, which no resume delivers", n.Owed)
	}
	return n, nil
}

// endNotConverged ends the invocation as NOT_CONVERGED. The task is not
// finished: the candidate stands and a resume re-plans it.
func (e *Engine) endNotConverged(taskID string, n NotConverged) {
	e.noteNotConverged(taskID, n)
	e.emitRunTerminal(taskID, event.WorkflowNotConverged, event.SourceSystem,
		runreceipt.OutcomeNotConverged, e.candidateStateFor(taskID),
		"the candidate did not converge: "+n.Describe()+
			". The task and its candidate are preserved; resume it to have the architect re-plan", n)
}

// replanPrompt asks the architect for a revised bounded plan for a candidate
// that did not converge, or whose re-plan was blocked. It is the escalation
// prompt's sibling: the same contract, a different reason.
func replanPrompt(task, plan, why, openFindings string) string {
	findings := strings.TrimSpace(openFindings)
	if findings == "" {
		findings = "(the record carries no review text)"
	}
	return fmt.Sprintf(`The bounded implementation of this task is owed an architectural re-plan: %s

The candidate is preserved and already contains the implementers' work. Revise the plan with your architectural authority so that the open findings below are resolved by design, not patched one instance at a time: if the same class of finding kept recurring, the plan should route that concern through one owner. Escalate to the human only if the decision changes human-owned intent/policy/contract/trust authority. Otherwise issue a revised bounded plan.

TASK:
%s

CURRENT PLAN:
%s

OPEN FINDINGS (latest independent review):
%s

Return ONLY the same architecture JSON contract as before.`, why, task, plan, findings)
}

// owedReplan reports whether a planned task's resume must begin with an
// architect re-plan, and why. A non-convergence record owes one; so does an
// architect turn that was blocked on a planned task (a re-plan inside a cycle).
func owedReplan(notConverged, blocked json.RawMessage, taskID string) (string, bool, error) {
	if len(notConverged) != 0 {
		n, err := ParseNotConverged(notConverged)
		if err == nil && n.TaskID != taskID {
			err = fmt.Errorf("the non-convergence record is bound to task %s, not to this one", n.TaskID)
		}
		if err != nil {
			return "", false, err
		}
		return "the candidate did not converge: " + n.Describe(), true, nil
	}
	if len(blocked) != 0 {
		if b, err := ParseExternalBlock(blocked); err == nil && b.Role == string(roles.Architect) {
			return "the architect re-plan this task was owed was blocked (" + b.Describe() + ")", true, nil
		}
	}
	return "", false, nil
}

// A CYCLE THAT PRODUCED WHAT THE REVIEW DEMANDED HAS CONVERGED, EVEN WHEN WHAT
// THE REVIEW DEMANDED WAS NOT CODE.
//
// Measured on the DF-19 resume, 2026-09-25: a review returned an EVIDENCE
// finding, the implementer ran exactly the witnesses it asked for and captured
// the result, the diff was rightly unchanged -- and the run ended "the candidate
// did not change between review cycles". The proof lived only in a transcript,
// so the next cycle would have had to produce it again. A system whose only
// durable cycle output is a diff cannot tell "produced nothing" from "produced
// exactly what was asked for".
//
// So evidence that discharges a reviewer-owned EVIDENCE finding is retained as
// its own durable event, bound to the task, the finding, the candidate the
// review was raised on and the candidate the check ran on, BEFORE the finding
// is credited. A code or scope finding is never discharged by it: those stay
// file-change obligations, and a cycle that produced evidence while one is
// still owed has its own diagnosis, distinct from producing nothing.

// RetainedEvidence is the payload of event.FindingEvidenceRetained.
type RetainedEvidence struct {
	TaskID    string `json:"task_id"`
	FindingID string `json:"finding_id"`
	// FindingClass is the reviewer's class, copied from the finding. Only
	// roles.EvidenceFinding is ever retained or read back.
	FindingClass roles.FindingClass `json:"finding_class"`
	// FindingDigest is the content identity of the finding answered. Ids
	// repeat across reviews ("f1" every time), so the id alone would let proof
	// for one review's f2 discharge a different review's f2.
	FindingDigest string `json:"finding_digest"`
	// ReviewedDigest and ReviewedTree are the candidate the finding was
	// raised on. A record is read back only for the open review raised on
	// this exact candidate: proof one review demanded is not proof for
	// another, however alike their findings read.
	ReviewedDigest string `json:"reviewed_candidate_digest"`
	ReviewedTree   string `json:"reviewed_candidate_tree"`
	// CandidateDigest and CandidateTree are the candidate the check ran on.
	// A record is read back only for this exact candidate.
	CandidateDigest string `json:"candidate_digest"`
	CandidateTree   string `json:"candidate_tree"`
	// Check is the executed validation record, as the broker ran it.
	Check validation.Evidence `json:"check"`
}

// key is the stable binding a duplicate is recognised by: the same finding of
// the same review, answered on the same candidate, by the same check. Timestamps and output
// are deliberately not part of it -- a re-run of the same check on the same
// bytes is the same proof, not a second one.
func (r RetainedEvidence) key() string {
	return strings.Join([]string{r.TaskID, r.FindingID, r.FindingDigest, r.ReviewedDigest, r.ReviewedTree, r.CandidateTree, r.CandidateDigest, commandLine(r.Check)}, "\x1f")
}

// Describe is the one line the durable event carries.
func (r RetainedEvidence) Describe() string {
	return fmt.Sprintf("retained the evidence for review finding [%s] (%s): %q %s on candidate tree %s",
		r.FindingID, r.FindingClass, commandLine(r.Check), r.Check.Outcome, shortDigest(r.CandidateTree))
}

// ParseRetainedEvidence reads a retained record back, refusing one that could
// discharge anything other than an EVIDENCE finding or that is not bound to a
// finding, the candidate its review was raised on, and the candidate its check
// ran on.
func ParseRetainedEvidence(raw json.RawMessage) (RetainedEvidence, error) {
	var r RetainedEvidence
	if err := json.Unmarshal(raw, &r); err != nil {
		return RetainedEvidence{}, fmt.Errorf("the retained evidence record is unreadable: %w", err)
	}
	switch {
	case r.FindingClass != roles.EvidenceFinding:
		return RetainedEvidence{}, fmt.Errorf("the retained evidence record is for a %q finding; only an evidence finding is discharged by evidence", r.FindingClass)
	case strings.TrimSpace(r.TaskID) == "" || strings.TrimSpace(r.FindingID) == "" || strings.TrimSpace(r.FindingDigest) == "":
		return RetainedEvidence{}, errors.New("the retained evidence record is not bound to a task and a finding")
	case strings.TrimSpace(r.ReviewedTree) == "" || strings.TrimSpace(r.ReviewedDigest) == "":
		return RetainedEvidence{}, errors.New("the retained evidence record is not bound to the reviewed candidate")
	case strings.TrimSpace(r.CandidateTree) == "" || strings.TrimSpace(r.CandidateDigest) == "":
		return RetainedEvidence{}, errors.New("the retained evidence record is not bound to a candidate")
	case r.Check.Outcome != validation.Passed || commandLine(r.Check) == "":
		return RetainedEvidence{}, errors.New("the retained evidence record carries no passing executed check")
	}
	return r, nil
}

// findingDigest is the content identity of a finding, as the reviewer wrote it.
func findingDigest(f roles.Finding) string {
	raw, _ := json.Marshal(f)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// retainedFor is the retained record that discharges f, if there is one: the
// same finding content and a check f's proof gap names. This is THE class
// boundary for retained evidence: only an EVIDENCE finding can be matched, so
// no record, however bound, discharges a code or scope finding. The caller has
// already narrowed records to boundTo this task, open review and candidate.
func retainedFor(f roles.Finding, retained []RetainedEvidence) (RetainedEvidence, bool) {
	if f.Class != roles.EvidenceFinding {
		return RetainedEvidence{}, false
	}
	required := strings.TrimSpace(f.ProofGap + " " + f.Correction)
	digest := findingDigest(f)
	for _, r := range retained {
		if r.FindingClass == roles.EvidenceFinding && r.FindingID == f.ID && r.FindingDigest == digest &&
			required != "" && strings.Contains(required, commandLine(r.Check)) {
			return r, true
		}
	}
	return RetainedEvidence{}, false
}

// boundTo is whether r was retained for exactly this task, the open review
// raised on exactly this reviewed candidate, and exactly this candidate. Every
// identity field must match: a record from another review, or for other bytes,
// is not recovered even when its finding and check read the same.
func (r RetainedEvidence) boundTo(taskID string, open openReview, candidateDigest, tree string) bool {
	return r.TaskID == taskID &&
		r.ReviewedDigest != "" && r.ReviewedDigest == open.CandidateDigest &&
		r.ReviewedTree != "" && r.ReviewedTree == open.CandidateTree &&
		r.CandidateDigest != "" && r.CandidateDigest == candidateDigest &&
		r.CandidateTree != "" && r.CandidateTree == tree
}

// errNoDurableRecord is why evidence cannot be retained without a session store.
var errNoDurableRecord = errors.New("this engine has no durable session record to retain evidence in")

// retainedEvidence reads back, from the durable session record, every evidence
// record retained for this task, for the open review's exact reviewed
// candidate, on this exact candidate.
func (e *Engine) retainedEvidence(taskID string, open openReview, candidateDigest, tree string) ([]RetainedEvidence, error) {
	if e.Store == nil {
		return nil, errNoDurableRecord
	}
	events, err := e.Store.Load()
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the retained evidence back: %w", err)
	}
	var out []RetainedEvidence
	for _, ev := range events {
		if ev.TaskID != taskID || ev.Kind != event.FindingEvidenceRetained {
			continue
		}
		r, err := ParseRetainedEvidence(ev.Payload)
		if err != nil || !r.boundTo(taskID, open, candidateDigest, tree) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// retainEvidence writes one record durably, or says why it could not. The
// write is checked, unlike emit's: evidence that was not retained is not
// credited, so a failed append must be seen by the caller.
func (e *Engine) retainEvidence(r RetainedEvidence) error {
	if e.Store == nil {
		return errNoDurableRecord
	}
	ev := event.New(e.SessionID, r.TaskID, event.SourceSystem, event.FindingEvidenceRetained, r.Describe(), r)
	if err := e.Store.Append(ev); err != nil {
		return err
	}
	if e.Bus != nil {
		e.Bus.Publish(ev)
	}
	return nil
}

// retainDischarges makes every evidence discharge in account durable before
// any of it is credited, and returns the records the discharges rest on.
//
// A discharge recovered from an earlier record is not written again, and that
// is the whole of idempotence: accountForFindingsWith prefers a retained record
// over a fresh answer, so the same binding on the same candidate is always
// recovered rather than re-derived. Any write that fails fails the account
// closed: the caller must not credit a finding whose proof did not land.
func (e *Engine) retainDischarges(taskID string, open openReview, candidateDigest, tree string, account findingAccount) ([]RetainedEvidence, error) {
	var out []RetainedEvidence
	if len(account.Evidence) != 0 && (strings.TrimSpace(open.CandidateDigest) == "" || strings.TrimSpace(open.CandidateTree) == "") {
		return nil, errors.New("the open review names no exact reviewed candidate, so evidence for its findings cannot be bound and is not credited")
	}
	for _, d := range account.Evidence {
		if d.Retained != nil {
			out = append(out, *d.Retained)
			continue
		}
		r := RetainedEvidence{
			TaskID: taskID, FindingID: d.Finding.ID, FindingClass: d.Finding.Class,
			FindingDigest:  findingDigest(d.Finding),
			ReviewedDigest: open.CandidateDigest, ReviewedTree: open.CandidateTree,
			CandidateDigest: candidateDigest, CandidateTree: tree,
			Check: d.Check,
		}
		if err := e.retainEvidence(r); err != nil {
			return nil, fmt.Errorf("the evidence produced for review finding [%s] could not be retained durably, so it is not credited: %w", r.FindingID, err)
		}
		out = append(out, r)
	}
	return out, nil
}

// withRetainedEvidence folds the records a cycle's accounting rests on into
// the evidence identity a verdict is reached on. Retained evidence answering a
// finding IS the evidence changing, so an ACCEPT after it answers the open
// review instead of contradicting it; the same records on a later cycle give
// the same identity, so a verdict still cannot flip on nothing.
func withRetainedEvidence(identity string, retained []RetainedEvidence) string {
	if len(retained) == 0 {
		return identity
	}
	keys := make([]string, 0, len(retained)+1)
	keys = append(keys, identity)
	for _, r := range retained {
		keys = append(keys, r.key())
	}
	sum := sha256.Sum256([]byte(strings.Join(keys, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Two non-convergence diagnoses the operator must be able to tell apart. The
// first is the existing identical-diff sentence (see runCandidate): a cycle
// that produced no durable response at all. This is the other one.
const producedEvidenceNotCode = "the cycle produced the evidence the review required, retained durably, and left a finding that requires a change unanswered; evidence does not discharge a code or scope finding"

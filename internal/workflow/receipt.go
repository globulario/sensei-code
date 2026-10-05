package workflow

// The governed run's own account of itself.
//
// C5 died because an external apparatus tried to reconstruct what the governor
// already knew, from a general-purpose event stream, after the fact. Every fact
// the witness had to rebuild is one this engine holds at the moment it acts. So
// the engine records each fact WHEN IT MEASURES IT, and emits one receipt at
// every terminal path.
//
// Two rules keep this from drifting back into reconstruction:
//
//   - Nothing here derives a fact from another fact. A field the run did not
//     measure stays UNKNOWN with the reason it was not measured. An engine that
//     infers is an engine that reconstructs, one field at a time.
//   - Every terminal path states its Outcome and CandidateState explicitly, by
//     signature. A new terminal path cannot inherit somebody else's answer.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/runreceipt"
)

// receiptFacts is what one task has measured so far.
//
// Its zero state is never used: beginReceipt authors an explicit reason for
// every field, so a receipt emitted from a run that ended early says WHY each
// fact is missing rather than carrying a blank.
type receiptFacts struct {
	base, graph, plan runreceipt.Value
	// candidateObservation is the ONE canonical account of the current
	// candidate: its existence, identity, rendering and provenance. Every
	// candidate field of a receipt is read from it, and it is only ever
	// replaced or enriched as a whole (DF-41A4).
	candidateObservation
	// reviewedTree and the observation's capturedTree are DIFFERENT FACTS and
	// were one field.
	//
	// The capture freezes a tree before anything judges it; a reviewed tree
	// exists only once a bounded verdict comes back bound to one. Writing the
	// capture into a field named "reviewed" made the mint use a remembered
	// PRE-review measurement while claiming it used the reviewed one.
	reviewedTree runreceipt.Value
	// deferredQuestion is the authority question a run left standing, and
	// executionBudget the deadline a timed-out invocation exhausted.
	deferredQuestion runreceipt.Value
	executionBudget  runreceipt.Value
	// externalBlock is the role turn a BLOCKED_EXTERNAL run is owed and the
	// provider condition that blocked it.
	externalBlock runreceipt.Value
	// notConverged is who spent which review budget, and what is owed.
	notConverged runreceipt.Value
	// restorationRefusal is the authority instrument whose binding a resume
	// could not read or verify.
	restorationRefusal runreceipt.Value
	// planAdmissionRefusal is the exact canonical plan-admission refusal an
	// invocation parked on, copied from its refusal record.
	planAdmissionRefusal                  runreceipt.PlanAdmissionRefusal
	formatterMutation                     runreceipt.Value
	provider, executable, verdict, digest runreceipt.Value
	serving                               runreceipt.Value
	attempts                              []runreceipt.Attempt
	// planState is a STATE, not a boolean, for the reason the observation's
	// candidateState is.
	planState runreceipt.PlanState
}

// beginReceipt opens the record for a task before anything is established.
//
// The reasons are authored once, here, and they describe the pre-measurement
// state truthfully: a run that fails at its first step emits a receipt saying
// it never reached the gate, which is a better record than one saying nothing.
func (e *Engine) beginReceipt(taskID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.receipts == nil {
		e.receipts = map[string]*receiptFacts{}
	}
	e.receipts[taskID] = freshFacts()
}

// freshFacts is the pre-measurement state, with an authored reason per field.
//
// It is NOT a convenience default: it never fills a gap after the fact, and
// nothing it produces can read as a measurement. It states, before the run
// starts, why each fact is not yet known -- so a run that dies at step one
// still emits a record that says what it never reached.
func freshFacts() *receiptFacts {
	notYet := func(what string) runreceipt.Value {
		return runreceipt.UnknownValue("not measured: " + what)
	}
	return &receiptFacts{
		base:  notYet("the run did not reach the start gate"),
		graph: notYet("the run did not reach the start gate"),
		plan:  notYet("no plan was established for this run"),
		// Opening state. Nothing has been CREATED yet, so the candidate axis
		// opens at NONE as a positive claim, with its absence recorded.
		candidateObservation: openingCandidate(),
		deferredQuestion:     notYet("no authority question was deferred"),
		executionBudget:      notYet("no execution budget expired"),
		externalBlock:        notYet("no role turn was blocked externally"),
		notConverged:         notYet("the run did not end unconverged"),
		// A run that never resumed anything refused no restoration, and says
		// so rather than carrying a blank.
		restorationRefusal: notYet("the run refused no restoration"),
		// Stated, like every other fact: a run that parked on no refusal says
		// so rather than carrying a blank.
		planAdmissionRefusal: runreceipt.PlanAdmissionRefusal{State: runreceipt.Unknown,
			Detail: "not measured: the run parked on no plan-admission refusal"},
		// Stated, not defaulted: a candidate that never reached validation has
		// an UNKNOWN formatter fact, and UNKNOWN is a value rather than a gap.
		formatterMutation: runreceipt.MeasuredValue(string(runreceipt.FormatterUnsaid),
			"validation had not run when this record was opened"),
		reviewedTree: notYet("no bounded review was delivered"),
		provider:     notYet("no reviewer was assigned"),
		executable:   notYet("the engine does not measure the reviewer executable"),
		verdict:      notYet("no bounded verdict was returned"),
		digest:       notYet("no bounded verdict was returned"),
		serving:      notYet("the awareness process had not been launched"),
		// The plan axis does NOT open at NONE: a supplied plan may already
		// exist before the first step succeeds, and a resumed task with no
		// restored record certainly cannot claim there is no plan. Opening at
		// NONE would have let an early failure deny a plan that exists. It
		// opens UNKNOWN and is asserted by whoever establishes it.
		planState: runreceipt.PlanUnknown,
	}
}

// withReceipt applies a measurement. It is a no-op for a task with no open
// record, so a code path that measures before beginRecord cannot panic a run.
func (e *Engine) withReceipt(taskID string, apply func(*receiptFacts)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if f, ok := e.receipts[taskID]; ok && f != nil {
		apply(f)
	}
}

// noteWorld records the base and the graph the start gate certified.
// noteWorld records the base and the digest of the graph actually served.
//
// The graph BUILD COMMIT is a different fact and must not stand in for the
// digest: one names the generation that produced the rules, the other the bytes
// that answered this run. An earlier draft put the build commit into
// GraphDigest, which is one measured fact carrying a different claim -- the
// exact pattern this chain keeps repairing.
func (e *Engine) noteWorld(taskID, base, graphDigest string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		f.base = runreceipt.MeasuredValue(base, "git rev-parse HEAD, at the certified start")
		if strings.TrimSpace(graphDigest) == "" {
			f.graph = runreceipt.UnknownValue(
				"the certified start did not carry a live graph digest; the build commit is a different fact and does not stand in for it")
			return
		}
		f.graph = runreceipt.MeasuredValue(graphDigest, "sensei preflight authority.live_store_graph_digest_sha256")
	})
}

// notePlanAbsent asserts that this run carries no plan at all.
//
// A conversational lane never plans, and saying so is a measurement of the
// lane's shape rather than a default. It is separate from notePlan so that "no
// plan" is always something a caller CLAIMED, never something a struct's zero
// value implied.
func (e *Engine) notePlanAbsent(taskID string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		f.planState = runreceipt.PlanNone
		f.plan = runreceipt.UnknownValue("this lane carries no plan")
	})
}

// notePlan records the identity of the bound this run carried.
//
// Every plan, architect-authored or supplied, is named by its canonical
// PlanAttemptID (planAttemptID) -- the one identity every plan-local authority
// binds to, re-derivable from the recorded plan, and binding the task,
// objective, world, complete payload, source and supplied digest together. A
// digest of the prose, or of the supplied bytes alone, would be a second plan
// identity that two different attempts could share. A supplied plan's byte
// digest is kept beside it as provenance: it says where the plan came from,
// not which attempt this run carried.
func (e *Engine) notePlan(taskID, suppliedDigest, planAttemptID string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		supplied := strings.TrimSpace(suppliedDigest)
		switch {
		case strings.TrimSpace(planAttemptID) != "":
			f.planState = runreceipt.PlanPresent
			if supplied != "" {
				f.plan = runreceipt.MeasuredValue(planAttemptID, "the canonical PlanAttemptID of the supplied plan "+
					"(sha256 of the complete plan attempt identity); provenance: the supplied bytes, sha256 "+supplied+", as handed in")
				return
			}
			f.plan = runreceipt.MeasuredValue(planAttemptID, "the canonical PlanAttemptID of the architect's plan (sha256 of the complete plan attempt identity)")
		case supplied != "":
			// A supplied plan recorded before plan-attempt identity: present,
			// and its attempt identity is not minted after the fact.
			f.planState = runreceipt.PlanPresent
			f.plan = runreceipt.UnknownValue("the supplied plan (sha256 " + supplied + ", as handed in) was recorded before " +
				"canonical plan-attempt identity; no identity is minted for it")
		default:
			// A conversational answer carries no plan. NONE is the claim, and
			// the digest is the recorded absence that claim requires.
			f.planState = runreceipt.PlanNone
			f.plan = runreceipt.UnknownValue("no plan was produced for this run")
		}
	})
}

// notePlanUnidentified records that this run carries a plan whose record
// predates plan-attempt identity. The plan is present and its identity is
// UNKNOWN: none is minted for an already-recorded plan.
func (e *Engine) notePlanUnidentified(taskID string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		f.planState = runreceipt.PlanPresent
		f.plan = runreceipt.UnknownValue("the resumed plan was recorded before canonical plan-attempt identity; no identity is minted for it")
	})
}

// noteCandidateDigest enriches the current candidate observation with the
// identity of its content.
//
// The source names where the digest is measured: the certified capture, which
// noteCertifiedCandidate records before any inspection can end the run. It must
// not name the review binding, which a run refused before review never builds.
//
// A digest that differs from one already measured describes different content:
// the observation keeps its existence and drops every identity measured of the
// earlier content, so the two are never merged (DF-41A4).
func (e *Engine) noteCandidateDigest(taskID, digest string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		o := f.candidateObservation
		if o.candDiff.State == runreceipt.Known && o.candDiff.Text != digest {
			o = o.contentChanged("the candidate's content changed to diff sha256 " + digest + " after its earlier identity was measured")
		}
		o.candDiff = runreceipt.MeasuredValue(digest, candidateDigestSource)
		f.replaceCurrentCandidate(o)
	})
}

// candidateDigestSource is the provenance of the validated candidate digest.
const candidateDigestSource = "sha256 of the canonical diff of the certified post-validation frozen capture"

// noteCertifiedCandidate records, in one step, what capture certification
// established: the validated diff digest, the frozen tree, and that a
// candidate holding work exists.
//
// It runs IMMEDIATELY after certification and before any inspection or audit
// that can end the run. DF-35: these facts used to be recorded only once every
// inspection had passed, so a run that failed at prospective inspection after
// committing and validating a candidate emitted a receipt saying no candidate
// was created.
//
// It REPLACES the current observation whole (DF-41A4). A certified capture is
// the current candidate, so nothing measured of an earlier one -- its digest,
// its tree, its certification -- survives beside it. A capture whose tree is
// the base tree holds no change: there is no current candidate, and a candidate
// certified earlier is not kept PRESENT merely because it once existed.
func (e *Engine) noteCertifiedCandidate(taskID, base, digest, tree, baseTree string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		if tree != "" && tree == baseTree {
			f.replaceCurrentCandidate(emptyCandidate(tree))
			return
		}
		f.replaceCurrentCandidate(certifiedCandidate(base, digest, tree))
	})
}

// noteCaptureRefused records the current candidate when its post-validation
// capture was measured and then refused certification: a capture holding no
// change is no current candidate, and one holding work is UNATTEMPTED.
func (e *Engine) noteCaptureRefused(taskID, tree, baseTree string) {
	if tree != "" && tree == baseTree {
		e.withReceipt(taskID, func(f *receiptFacts) { f.replaceCurrentCandidate(emptyCandidate(tree)) })
		return
	}
	e.noteCandidateUnattempted(taskID, tree)
}

// noteCurrentCapture records, immediately, what a capture of the current
// candidate -- what names which one -- measured, before any refusal,
// validation, later capture or terminal can follow it (DF-41A4).
//
// A capture holding no change is no current candidate, whatever an earlier
// cycle established. A capture holding work is the current candidate: PRESENT,
// established by the mutable worktree's frozen tree and by nothing else, with
// no identity until certification or the mint measures one. An observation
// already holding exactly that content is the same candidate measured more
// strongly, and it stands; any other is replaced whole.
func (e *Engine) noteCurrentCapture(taskID, what, base, tree, diff string) {
	in := candidateInput{What: what, Base: base, Tree: tree, Empty: strings.TrimSpace(diff) == ""}
	e.withReceipt(taskID, func(f *receiptFacts) {
		o := f.candidateObservation
		if !in.Empty && o.candidateState == runreceipt.CandidatePresent && o.unmeasured == "" &&
			o.capturedTree.State == runreceipt.Known && o.capturedTree.Text == tree {
			return
		}
		next, err := observedCandidate(in)
		if err != nil {
			next = candidateWith(runreceipt.CandidateUnknown, what+" could not be observed: "+err.Error())
			next.candBase, next.unmeasured = f.candBase, what+" could not be observed: "+err.Error()
		}
		f.replaceCurrentCandidate(next)
	})
}

// observeCurrentCandidate captures the worktree and records what it measured
// -- work, no change, or the capture's own failure -- as the current
// candidate. It returns nothing: the caller's outcome is its own, and this
// only keeps the observation current before that outcome is routed.
func (e *Engine) observeCurrentCandidate(ctx context.Context, taskID string, tc *taskContext, workspace, what string) {
	capture, err := gitx.Repo{Root: workspace}.CandidateCapture(ctx, tc.Identity.BaseSHA, tc.Files)
	if err != nil {
		e.noteCandidateCaptureFailed(taskID, what, err)
		return
	}
	e.noteCurrentCapture(taskID, what, tc.Identity.BaseSHA, capture.Tree, capture.Diff)
}

// noteCandidateCaptureFailed records that a capture of the current candidate
// -- what names which one -- could not be measured. It is the one owner of the
// candidate truth a failed capture leaves behind.
//
// A capture failure is not absence, and it is not presence either: an
// attempted capture establishes nothing. The worktree it could not read may
// still hold the prior candidate, a replacement, or nothing. The prior
// observation could stand whole only if something mechanically showed it is
// still the current candidate, and a failed capture measures nothing that
// could; so the current observation is replaced, whole, by one UNKNOWN
// observation whose every candidate field carries the exact failure. It is
// never NONE, never PRESENT, and keeps no identity of the prior candidate, nor
// is any ref read at the terminal merged into it.
func (e *Engine) noteCandidateCaptureFailed(taskID, what string, err error) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		next, cerr := observedCandidate(candidateInput{What: what, Base: f.candBase, Failure: err.Error()})
		if cerr != nil {
			why := what + " failed: " + err.Error()
			next = candidateWith(runreceipt.CandidateUnknown, why)
			next.candBase, next.unmeasured = f.candBase, why
		}
		f.replaceCurrentCandidate(next)
	})
}

// candidateInput is one measurement of the current candidate, as a durable
// checkpoint stores it (70B1): what measured it, the base it was cut from, and
// either the tree the capture froze -- with whether it holds any change -- or
// the capture's own failure. It is an input, never an observation: only
// observedCandidate turns it into one.
type candidateInput struct {
	What    string `json:"what"`
	Base    string `json:"base,omitempty"`
	Tree    string `json:"tree,omitempty"`
	Empty   bool   `json:"empty,omitempty"`
	Failure string `json:"failure,omitempty"`
}

// observedCandidate is the ONE construction of the current candidate
// observation from a capture's measurement: the live capture transitions use
// it, and so does a checkpoint's replay. A measurement it cannot construct an
// observation from is refused, never repaired.
//
//   - a failed capture establishes nothing: UNKNOWN, every field carrying the
//     failure, and no tree -- a failure that also states a tree is refused;
//   - a capture holding no change from the base is no current candidate: NONE;
//   - a capture holding work is PRESENT and uncertified, with the tree it froze.
func observedCandidate(in candidateInput) (candidateObservation, error) {
	what := strings.TrimSpace(in.What)
	switch {
	case what == "":
		return candidateObservation{}, errors.New("the candidate measurement names no capture")
	case in.Failure != "":
		if in.Tree != "" || in.Empty {
			return candidateObservation{}, errors.New("the candidate measurement is a failed capture that also states what it captured")
		}
		why := in.What + " failed: " + in.Failure
		o := candidateWith(runreceipt.CandidateUnknown, why)
		o.candBase, o.unmeasured = in.Base, why
		return o, nil
	case strings.TrimSpace(in.Tree) == "":
		return candidateObservation{}, errors.New("the candidate measurement is a capture that froze no tree")
	case in.Empty:
		return candidateWith(runreceipt.CandidateNone,
			"no current candidate: "+in.What+" (tree "+in.Tree+") holds no candidate change from the base"), nil
	}
	o := candidateWith(runreceipt.CandidatePresent, "the current candidate (tree "+in.Tree+
		") was measured by "+in.What+" and has not been certified")
	o.capturedTree = runreceipt.MeasuredValue(in.Tree, "the canonical tree the capture froze")
	o.candBase = strings.TrimSpace(in.Base)
	return o, nil
}

// checkpointCapture names the capture a checkpoint measures its candidate by.
const checkpointCapture = "the checkpoint's capture of the current candidate"

// measureCandidate captures the current candidate for a checkpoint. It records
// nothing: the receipt's observation is not touched.
func (e *Engine) measureCandidate(ctx context.Context, tc *taskContext, workspace string) candidateInput {
	in := candidateInput{What: checkpointCapture}
	if tc == nil {
		in.Failure = "no task context names the candidate"
		return in
	}
	in.Base = tc.Identity.BaseSHA
	if strings.TrimSpace(workspace) == "" {
		in.Failure = "no candidate workspace is known"
		return in
	}
	capture, err := gitx.Repo{Root: workspace}.CandidateCapture(ctx, tc.Identity.BaseSHA, tc.Files)
	if err != nil {
		in.Failure = err.Error()
		return in
	}
	in.Tree, in.Empty = capture.Tree, strings.TrimSpace(capture.Diff) == ""
	return in
}

// candidateRecord is the canonical form of one candidate observation: what a
// checkpoint's ReplayDigest covers of it.
type candidateRecord struct {
	State          runreceipt.CandidateState `json:"state"`
	Commit         runreceipt.Value          `json:"commit"`
	Tree           runreceipt.Value          `json:"tree"`
	Parent         runreceipt.Value          `json:"parent"`
	Diff           runreceipt.Value          `json:"diff"`
	CapturedTree   runreceipt.Value          `json:"captured_tree"`
	Rendering      runreceipt.Value          `json:"rendering"`
	DigestRelation runreceipt.DigestRelation `json:"digest_relation"`
	Certified      bool                      `json:"certified"`
	Base           string                    `json:"base,omitempty"`
	Unmeasured     string                    `json:"unmeasured,omitempty"`
}

func (o candidateObservation) record() candidateRecord {
	return candidateRecord{
		State: o.candidateState, Commit: o.candCommit, Tree: o.candTree, Parent: o.candParent, Diff: o.candDiff,
		CapturedTree: o.capturedTree, Rendering: o.candRendering, DigestRelation: o.digestRelation,
		Certified: o.certified, Base: o.candBase, Unmeasured: o.unmeasured,
	}
}

// Which capture failed, named in the reason every identity field carries.
const (
	postWorkerCapture     = "the capture of the current candidate after the implementer's turn"
	postValidationCapture = "the post-validation recapture of the current candidate"
	postFormatCapture     = "the recapture of the current candidate after validation's formatters"
	erroredWorkerCapture  = "the capture of the current candidate after the implementer's turn returned an error"
)

// noteCapturedTree enriches the current candidate observation with the content
// identity the capture froze.
//
// This is NOT the reviewed tree. Nothing has judged it yet, and a field that
// conflated the two let the mint use a pre-review measurement while reporting
// it as the reviewed one. A tree that differs from one already captured is
// different content, and nothing measured of the earlier content is kept.
func (e *Engine) noteCapturedTree(taskID, tree string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		o := f.candidateObservation
		if o.capturedTree.State == runreceipt.Known && o.capturedTree.Text != tree {
			o = o.contentChanged("the candidate's content changed to tree " + tree + " after its earlier identity was measured")
		}
		o.capturedTree = runreceipt.MeasuredValue(tree, "the canonical tree the capture froze")
		f.replaceCurrentCandidate(o)
	})
}

// noteCandidateCommit records the accepted candidate's Git identity.
//
// It was called nowhere when the receipt first shipped, and that absence WAS
// F1: the loop left its candidate uncommitted, so a PRESENT candidate could
// never state its commit, tree or first parent and the main success path could
// not produce a complete account of itself. mintCandidateIdentity calls it now.
//
// A minted identity establishes the candidate. It enriches the current
// observation only when the minted tree is the captured tree; otherwise the two
// are not shown to be one candidate, and the minted object, the stronger
// observation, replaces the captured one whole (DF-41A4).
func (e *Engine) noteCandidateCommit(taskID, commit, tree, firstParent string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		o := f.candidateObservation
		if o.capturedTree.State == runreceipt.Known && o.capturedTree.Text != tree {
			rendering, relation := o.candRendering, o.digestRelation
			o = candidateWith(runreceipt.CandidatePresent, "the minted identity holds tree "+tree+
				", not the captured tree "+o.capturedTree.Text+", so no measurement of the captured content describes it")
			o.candRendering, o.digestRelation = rendering, relation
		}
		o.candidateState = runreceipt.CandidatePresent
		o.candCommit = runreceipt.MeasuredValue(commit, "git rev-parse on the candidate ref")
		o.candTree = runreceipt.MeasuredValue(tree, "git rev-parse <candidate>^{tree}")
		o.candParent = runreceipt.MeasuredValue(firstParent, "git rev-parse <candidate>^1")
		f.replaceCurrentCandidate(o)
	})
}

// noteReviewerAssigned opens one attempt. Delivery is UNKNOWN until a verdict
// arrives: an assignment is not a delivery, and the engine does not infer a
// failure from a replacement.
func (e *Engine) noteReviewerAssigned(taskID, provider string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		p := runreceipt.MeasuredValue(provider, "the reviewer role assignment this run made")
		f.provider = p
		f.attempts = append(f.attempts, runreceipt.Attempt{
			Provider: p,
			Delivery: runreceipt.UnknownValue("this attempt had not returned when it was superseded"),
			Verdict:  runreceipt.UnknownValue("this attempt produced no verdict"),
			Digest:   runreceipt.UnknownValue("this attempt produced no verdict"),
			Tree:     runreceipt.UnknownValue("this attempt produced no verdict"),
		})
	})
}

// noteReviewDelivered records a bounded verdict against the attempt that gave
// it, so the receipt's top-level review and its trail cannot disagree.
func (e *Engine) noteReviewDelivered(taskID, provider, decision, candidateDigest, reviewedTree string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		p := runreceipt.MeasuredValue(provider, "the provider recorded in the verdict's provenance")
		v := runreceipt.MeasuredValue(decision, "the reviewer's own decision")
		d := runreceipt.MeasuredValue(candidateDigest, "the candidate digest the verdict names")
		f.provider, f.verdict, f.digest = p, v, d
		// The reviewed tree comes from the VERDICT's envelope, so it exists
		// only once a bounded review has come back carrying one.
		if strings.TrimSpace(reviewedTree) != "" {
			f.reviewedTree = runreceipt.MeasuredValue(reviewedTree, "the candidate tree the verdict's envelope names")
		}
		tv := runreceipt.UnknownValue("this verdict's envelope named no tree")
		if strings.TrimSpace(reviewedTree) != "" {
			tv = runreceipt.MeasuredValue(reviewedTree, "the candidate tree the verdict's envelope names")
		}
		if n := len(f.attempts); n > 0 {
			f.attempts[n-1].Provider = p
			f.attempts[n-1].Delivery = runreceipt.DeliveryValue(runreceipt.Delivered, "the verdict this attempt returned")
			f.attempts[n-1].Verdict = v
			f.attempts[n-1].Digest = d
			f.attempts[n-1].Tree = tv
			return
		}
		f.attempts = append(f.attempts, runreceipt.Attempt{
			Provider: p,
			Delivery: runreceipt.DeliveryValue(runreceipt.Delivered, "the verdict this attempt returned"),
			Verdict:  v, Digest: d, Tree: tv,
		})
	})
}

// emitReceipt is the terminal boundary: one receipt, at the end of one run.
//
// Outcome and CandidateState are parameters rather than derived state, so a new
// terminal path must decide both. Deriving them here would be the convenience
// that lets the next author skip the question, and the question is the point.
// The CandidateState a call site passes is a consistency check, never a source:
// every candidate field is projected from the current candidate observation
// alone, and a call site that claims another state is reported beside the
// receipt as a disagreement, not written into it (see terminalCandidate).
func (e *Engine) emitReceipt(taskID string, terminal event.Kind, outcome runreceipt.Outcome, candState runreceipt.CandidateState) runreceipt.Receipt {
	e.mu.Lock()
	f := e.receipts[taskID]
	unrecorded := f == nil
	if unrecorded {
		// A terminal reached without an open record still emits one, and it
		// says so field by field rather than carrying invalid blanks.
		f = freshFacts()
	}
	facts := *f
	if unrecorded {
		// Nobody observed this task's candidate, so the observation says
		// that, as candidateStateFor does: UNKNOWN, not the opening NONE and
		// not whatever the call site claims.
		facts.candidateObservation = candidateWith(runreceipt.CandidateUnknown,
			"no receipt record was open for this task, so nothing about its candidate was observed")
	}
	e.mu.Unlock()

	// Every candidate field below is read from this ONE observation -- the
	// current one, as it stands at the moment the terminal is emitted -- so the
	// state and the identity fields cannot disagree about whether a candidate
	// exists, or about which candidate it is.
	cand, disagreement := e.terminalCandidate(taskID, facts, candState)
	governor, binary := governorIdentityFn()

	r := runreceipt.Receipt{
		Schema:                    runreceipt.SchemaVersion,
		GovernorCommit:            governor,
		GovernorBinarySHA256:      binary,
		BaseCommit:                facts.base,
		PlanDigest:                facts.plan,
		GraphDigest:               facts.graph,
		PlanState:                 facts.planState,
		ReviewedTree:              facts.reviewedTree,
		DeferredQuestion:          facts.deferredQuestion,
		ExecutionBudget:           facts.executionBudget,
		ExternalBlock:             facts.externalBlock,
		NotConverged:              facts.notConverged,
		RestorationRefusal:        facts.restorationRefusal,
		PlanAdmissionRefusal:      &facts.planAdmissionRefusal,
		FormatterMutationState:    facts.formatterMutation,
		CandidateCommitDiffDigest: cand.candRendering,
		CandidateDigestRelation:   cand.digestRelation,
		CandidateState:            cand.candidateState,
		CandidateCommit:           cand.candCommit,
		CandidateTree:             cand.candTree,
		CandidateFirstParent:      cand.candParent,
		CandidateDigest:           cand.candDiff,
		ServingProducer:           facts.serving,
		ReviewerProvider:          facts.provider,
		ReviewerExecutable:        facts.executable,
		ReviewVerdict:             facts.verdict,
		ReviewedDigest:            facts.digest,
		Attempts:                  facts.attempts,
		// The terminal EVENT, not the outcome. Recording the outcome here made
		// Outcome quietly into two fields, and a reader comparing them would
		// have been comparing a fact with itself.
		Terminal: runreceipt.MeasuredValue(string(terminal), "the terminal event this run emitted"),
		Outcome:  outcome,
	}
	state, missing := r.Completeness()
	payload := map[string]any{"receipt": r, "completeness": string(state), "missing": missing}
	if disagreement != "" {
		payload["candidate_state_disagreement"] = disagreement
	}
	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.RunReceipt,
		"governed run receipt: "+string(state)+" / "+string(outcome), payload))
	return r
}

// noteCandidateWork records whether the candidate holds WORK, measured.
//
// A worktree is an execution container, not a candidate: firing PRESENT when
// the directory was created made a run that produced nothing owe a commit, and
// minting an empty one to satisfy that would be a fabricated specimen in Git
// clothing. PRESENT and NONE are both read from the frozen tree.
//
// Work establishes existence and the content tree, and no identity. A PRESENT
// observation of the same content stands as it is; anything else is replaced
// whole, so no stale absence reason or earlier identity survives beside it.
func (e *Engine) noteCandidateWork(taskID, tree, baseTree string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		if tree != "" && tree == baseTree {
			f.replaceCurrentCandidate(emptyCandidate(tree))
			return
		}
		o := f.candidateObservation
		// Only a measured tree shows the content is the same: PRESENT work
		// whose tree nobody froze is not this work, and is replaced.
		if o.candidateState == runreceipt.CandidatePresent && o.unmeasured == "" &&
			o.capturedTree.State == runreceipt.Known && o.capturedTree.Text == tree {
			return
		}
		next := candidateWith(runreceipt.CandidatePresent,
			"the candidate holds work (tree "+tree+") and no identity of it has been measured")
		next.capturedTree = runreceipt.MeasuredValue(tree, "the canonical tree the capture froze")
		f.replaceCurrentCandidate(next)
	})
}

// noteCandidateWorkUnmeasured says the content is moving and unmeasured.
//
// A worker is editing, and a stale NONE here would deny work that exists. UNKNOWN
// is not absence, so no field says nothing was created.
func (e *Engine) noteCandidateWorkUnmeasured(taskID string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		f.replaceCurrentCandidate(candidateWith(runreceipt.CandidateUnknown,
			"the candidate worktree exists and its content has not been captured"))
	})
}

// noteInheritedCandidate records what a resumed invocation found on disk
// before it did anything: work is PRESENT, a clean worktree is NONE, and a
// worktree that could not be read is UNKNOWN rather than either claim. Nothing
// here measures the inherited content's tree, so no ref is ever stated as its
// identity: a ref is not shown to hold work nobody froze.
func (e *Engine) noteInheritedCandidate(taskID string, seen observation) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		switch {
		case seen.Err != nil:
			f.replaceCurrentCandidate(candidateWith(runreceipt.CandidateUnknown,
				"the inherited candidate worktree could not be read: "+seen.Err.Error()))
		case seen.DiffBytes > 0 || len(seen.ChangedPaths) > 0:
			f.replaceCurrentCandidate(candidateWith(runreceipt.CandidatePresent,
				"the inherited candidate holds work and this invocation has measured no identity of it"))
		default:
			f.replaceCurrentCandidate(candidateWith(runreceipt.CandidateNone,
				"no current candidate: the inherited worktree holds no change from its base"))
		}
	})
}

// noteCandidateUnattempted records that work exists and no canonical candidate
// identity will be created, because the run is refusing before the mint.
//
// A1's P1 witness found this hole: a capture-certification refusal returned
// after the worker had edited and validation had run, leaving candidate_state
// at the UNKNOWN the editing window had set. The receipt was INCOMPLETE while
// the execution knew exactly what had happened, which is the second candidate
// law -- evidence before dependent control flow -- failing in the seam that
// exists to enforce the first.
//
// It is a MEASUREMENT, not a default: callers reach it only on a path that has
// already decided to refuse. The refused work is the current candidate, so it
// replaces the observation whole: an earlier certified candidate's digest and
// certification are not kept beside it.
//
// The tree the refused capture froze is kept as what was measured of it, so a
// ref is read against the refused content and never in place of it.
func (e *Engine) noteCandidateUnattempted(taskID, tree string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		next := candidateWith(runreceipt.CandidateUnattempted,
			"the current candidate was refused certification, so no canonical identity is created for it")
		if strings.TrimSpace(tree) != "" {
			next.capturedTree = runreceipt.MeasuredValue(tree, "the canonical tree the refused capture froze")
		}
		next.candBase = f.candBase
		f.replaceCurrentCandidate(next)
	})
}

// noteCandidateRendering enriches the current observation with the canonical
// rendering of the MINTED object and how it compares with the rendering the
// review was given. mintCandidateIdentity records it with the minted identity.
func (e *Engine) noteCandidateRendering(taskID, digest string, reviewed string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		o := f.candidateObservation
		o.candRendering = runreceipt.MeasuredValue(digest, "sha256 of the canonical rendering of the minted object")
		switch {
		case digest == "" || reviewed == "":
			o.digestRelation = runreceipt.RelationUnknown
		case digest == reviewed:
			o.digestRelation = runreceipt.RelationMatch
		default:
			o.digestRelation = runreceipt.RelationDiffer
		}
		f.replaceCurrentCandidate(o)
	})
}

// candidateStateFor reports what the engine measured about the candidate's
// existence. NONE and PRESENT are both positive claims; a task with no open
// record yields UNKNOWN rather than a convenient NONE.
func (e *Engine) candidateStateFor(taskID string) runreceipt.CandidateState {
	e.mu.Lock()
	defer e.mu.Unlock()
	f, ok := e.receipts[taskID]
	if !ok || f == nil {
		return runreceipt.CandidateUnknown
	}
	return f.candidateState
}

// reviewedOutcome is the outcome of a path that ends with whatever the reviewer
// decided. Choosing it at a call site is a decision -- "this terminal is the
// review's" -- not a default, and it reads only what was measured.
func (e *Engine) reviewedOutcome(taskID string) runreceipt.Outcome {
	e.mu.Lock()
	defer e.mu.Unlock()
	f, ok := e.receipts[taskID]
	if !ok || f == nil || f.verdict.State != runreceipt.Known {
		return runreceipt.OutcomeUnreviewed
	}
	if runreceipt.ReviewDecision(f.verdict.Text) == runreceipt.DecisionAccept {
		return runreceipt.OutcomeAccepted
	}
	return runreceipt.OutcomeRefused
}

// governorIdentity is the running binary's account of itself.
//
// The schema requires it, and the engine could not state it: that was the
// finding, not the schema being demanding. Two of the three facts turned out to
// be measurable from inside the process, and the third -- the source commit --
// is embedded by the Go toolchain for any binary built from a checkout.
// governorIdentityFn is indirected so a test can isolate an axis it is not
// testing. Production never replaces it.
var governorIdentityFn = governorIdentity

func governorIdentity() (commit, binaryDigest runreceipt.Value) {
	commit = runreceipt.UnknownValue("this binary carries no VCS stamp; it was not built from a checkout")
	if info, ok := debug.ReadBuildInfo(); ok {
		var rev string
		var dirty bool
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		switch {
		case rev == "":
			// keep the authored reason above
		case dirty:
			// A commit does not identify a binary built from a modified tree.
			// Recording it anyway would be the strongest kind of false
			// precision: a governor naming a revision it is not.
			commit = runreceipt.UnknownValue(
				"built from a MODIFIED working tree at " + rev + "; that commit does not identify this binary")
		default:
			commit = runreceipt.MeasuredValue(rev, "runtime/debug build info vcs.revision")
		}
	}
	binaryDigest = fileDigest(osExecutable, "sha256 of the executable this process is running")
	return commit, binaryDigest
}

// osExecutable is indirected so a test can measure a known file instead of the
// test binary.
var osExecutable = func() (string, error) { return os.Executable() }

// fileDigest measures a file, or says why it could not.
func fileDigest(locate func() (string, error), source string) runreceipt.Value {
	path, err := locate()
	if err != nil {
		return runreceipt.UnknownValue("the path could not be resolved: " + err.Error())
	}
	f, err := os.Open(path)
	if err != nil {
		return runreceipt.UnknownValue("the file could not be read: " + err.Error())
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return runreceipt.UnknownValue("the file could not be digested: " + err.Error())
	}
	return runreceipt.MeasuredValue(hex.EncodeToString(h.Sum(nil)), source+" ("+path+")")
}

// noteAwarenessProducer records the executable this run launched to answer
// awareness.
//
// C5 found that a frozen "producer" field named a file nobody had shown to be
// executing. This one is the file this process actually launched, and the
// source says exactly that -- not "the producer", which would be a claim about
// a process rather than a measurement of an image.
func (e *Engine) noteServingProducer(taskID string, pid int, launched bool) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		if !launched || pid <= 0 {
			f.serving = runreceipt.UnknownValue("the awareness process did not start, so nothing served this run")
			return
		}
		exe := "/proc/" + strconv.Itoa(pid) + "/exe"
		if _, err := os.Stat(exe); err != nil {
			// Not every platform can name a running process's image. Saying so
			// is the measurement; substituting the file we intended to launch
			// would be an image standing in for a process.
			f.serving = runreceipt.UnknownValue(
				"the serving process (pid " + strconv.Itoa(pid) + ") answered, but this platform does not expose its executable: " + err.Error())
			return
		}
		f.serving = fileDigest(
			func() (string, error) { return exe, nil },
			"sha256 of pid "+strconv.Itoa(pid)+", the process that answered this run's awareness initialize")
	})
}

// emitRunTerminal is the ONE way a governed run ends.
//
// Receipt and terminal event are emitted together, in that order, so they
// cannot come apart. An earlier draft paired them by convention and guarded the
// pairing with a test that asked only whether a function contained BOTH calls
// somewhere -- which a function with three terminal exits and one receipt would
// have passed. Convention guarded by an approximate test is how the pairing
// would have drifted.
//
// Outcome and CandidateState stay call-site parameters: centralising the
// mechanism must not centralise the judgement, or a new terminal path inherits
// an answer instead of deciding one.
//
// Material a live production_scope refusal left pending (DF-48) is disposed of
// here, before anything of the terminal is emitted, so no exit can leave it
// retained by accident. A disposition that cannot be recorded suppresses the
// requested terminal and ends the run failed, naming that failure.
func (e *Engine) emitRunTerminal(taskID string, kind event.Kind, source event.Source,
	outcome runreceipt.Outcome, cand runreceipt.CandidateState, summary string, payload any) {
	if err := e.settleRefusedMaterial(taskID, kind, source, outcome); err != nil {
		e.emitRunTerminal(taskID, event.WorkflowFailed, event.SourceSystem,
			runreceipt.OutcomeFailed, e.candidateStateFor(taskID),
			"the "+string(kind)+" terminal was suppressed: "+err.Error(), nil)
		return
	}
	e.emitReceipt(taskID, kind, outcome, cand)
	e.emit(event.New(e.SessionID, taskID, source, kind, summary, payload))
}

// noteFormatterMutation records whether validation's formatter changed
// candidate bytes. Instrumentation only: it repairs nothing and decides
// nothing, it merely stops the occurrence from being unobservable.
func (e *Engine) noteFormatterMutation(taskID string, mutated bool) {
	state := runreceipt.FormatterUnchanged
	if mutated {
		state = runreceipt.FormatterMutated
	}
	e.withReceipt(taskID, func(f *receiptFacts) {
		f.formatterMutation = runreceipt.MeasuredValue(string(state),
			"the candidate diff digest compared across the formatter step")
	})
}

// noteNoFormatterConfigured records that nothing could have rewritten the
// candidate, which is a measurement rather than an absence of one.
func (e *Engine) noteNoFormatterConfigured(taskID string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		f.formatterMutation = runreceipt.MeasuredValue(string(runreceipt.FormatterUnchanged),
			"no formatter is configured, so nothing rewrote the candidate")
	})
}

// noteDeferredQuestion records the authority question a run stopped on.
//
// The subject is the question itself; the condition is the certifiability
// condition that produced the boundary. Both are recorded because an
// interruption a reader cannot trace back to a condition is one nobody learns
// from.
func (e *Engine) noteDeferredQuestion(taskID, subject, condition string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		text := strings.TrimSpace(subject)
		if c := strings.TrimSpace(condition); c != "" {
			text = strings.TrimSpace(text + " — " + c)
		}
		if text == "" {
			f.deferredQuestion = runreceipt.UnknownValue("the deferral recorded no question")
			return
		}
		f.deferredQuestion = runreceipt.MeasuredValue(text, "the authority decision the human declined to answer")
	})
}

// noteExternalBlock records the role turn a run was blocked on and why.
func (e *Engine) noteExternalBlock(taskID string, b ExternalBlock) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		f.externalBlock = runreceipt.MeasuredValue(b.Describe(),
			"the provider's structured refusal of the role turn this run was owed")
		// An architect turn that was never answered produced no plan, and that
		// is a fact, not an unknown. Only an unset state is claimed: a plan
		// already recorded -- an architect re-planning inside a cycle -- stands.
		if b.Role == string(roles.Architect) && f.planState == runreceipt.PlanUnknown {
			f.planState = runreceipt.PlanNone
			f.plan = runreceipt.UnknownValue("the architect turn was blocked before any plan was produced")
		}
	})
}

// noteNotConverged records the spent budget and what the task is owed.
//
// Uncertified work at this point was never minted and will not be in this
// invocation, which is exactly UNATTEMPTED. A CERTIFIED candidate is not
// downgraded (DF-35): it exists, and the terminal observation states it as
// PRESENT with the identity its ref holds. Only its mint evidence is UNKNOWN,
// which is the truth about a candidate that did not converge.
func (e *Engine) noteNotConverged(taskID string, n NotConverged) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		f.notConverged = runreceipt.MeasuredValue(n.Describe(),
			"the implementers whose review budgets were spent, and what the task is owed")
		if f.candidateState == runreceipt.CandidatePresent && !f.certified {
			f.candidateState = runreceipt.CandidateUnattempted
		}
	})
}

// noteRestorationRefusal records which instrument binding a resume could not
// read or verify.
//
// A candidate that holds work at this point was never minted and will not be in
// this invocation, which is exactly UNATTEMPTED: PRESENT would demand mint
// evidence that does not exist and make every such receipt INCOMPLETE, and an
// incomplete receipt for a preserved task is how a preserved task stops looking
// preserved.
func (e *Engine) noteRestorationRefusal(taskID string, r RestorationRefusal) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		f.restorationRefusal = runreceipt.MeasuredValue(r.Describe(),
			"the authority instrument whose binding this resume could not read or verify")
		if f.candidateState == runreceipt.CandidatePresent {
			f.candidateState = runreceipt.CandidateUnattempted
		}
	})
}

// notePlanAdmissionRefused records the exact canonical refusal an invocation
// parked on, copied losslessly from its refusal record, so the receipt emitted
// before the terminal event names it by itself. A candidate holding work at
// this point was never minted and will not be in this invocation: it is
// UNATTEMPTED, for the reason noteRestorationRefusal gives.
func (e *Engine) notePlanAdmissionRefused(taskID string, r planAttemptRefusal) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		f.planAdmissionRefusal = runreceipt.PlanAdmissionRefusal{
			State:               runreceipt.Known,
			Source:              "the canonical plan-admission refusal record this invocation parked on",
			PlanAttemptID:       r.PlanAttemptID,
			RefusalID:           r.RefusalID,
			Class:               runreceipt.PlanAdmissionRefusalClass(r.Class),
			Declaration:         string(r.Declaration),
			Reason:              r.Reason,
			GoverningEvidenceID: r.GoverningEvidenceID,
		}
		if f.candidateState == runreceipt.CandidatePresent {
			f.candidateState = runreceipt.CandidateUnattempted
		}
	})
}

// refuseRestoration ends the invocation as RESTORATION_REFUSED when err carries
// a typed restoration refusal, and reports whether it did.
//
// The task is left exactly as it stands: no candidate disposal, no handoff, no
// authority written, no record repaired. It is terminal for the INVOCATION and
// not for the TASK, which is the entire point -- the previous terminal for this
// condition removed the task from resume tooling for ever.
func (e *Engine) refuseRestoration(taskID string, err error) bool {
	var r *RestorationRefusal
	if !errors.As(err, &r) || r == nil {
		return false
	}
	e.noteRestorationRefusal(taskID, *r)
	e.emitRunTerminal(taskID, event.WorkflowRestorationRefused, event.SourceSystem,
		runreceipt.OutcomeRestorationRefused, e.candidateStateFor(taskID),
		"the authority this task recorded could not be re-established: "+r.Describe()+
			". Nothing was executed and no authority was written; the task is preserved and still resumable", *r)
	return true
}

// reviewedTreeFor returns the content identity a DELIVERED VERDICT was bound
// to, and whether one was delivered at all. It is deliberately not the captured
// tree: minting is for a candidate a reviewer judged.
func (e *Engine) reviewedTreeFor(taskID string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	f, ok := e.receipts[taskID]
	if !ok || f == nil || f.reviewedTree.State != runreceipt.Known {
		return "", false
	}
	return f.reviewedTree.Text, true
}

// candidateCommitFor returns the identity minted for this candidate, or "" if
// none was. Publication uses it to push the exact accepted object.
func (e *Engine) candidateCommitFor(taskID string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	f, ok := e.receipts[taskID]
	if !ok || f == nil || f.candCommit.State != runreceipt.Known {
		return ""
	}
	return f.candCommit.Text
}

// reviewedDigestFor returns the rendering digest the verdict named.
func (e *Engine) reviewedDigestFor(taskID string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	f, ok := e.receipts[taskID]
	if !ok || f == nil || f.digest.State != runreceipt.Known {
		return ""
	}
	return f.digest.Text
}

// candidateObservation is the ONE canonical account of the current candidate.
//
// DF-35: candidate_state was set from the measured tree while commit, tree and
// first parent were set only by the mint, so every non-accepted terminal after
// a candidate existed said PRESENT beside "no candidate was created". Two
// predicates disagreed about whether a candidate existed.
//
// DF-41A4: the fields were still written one at a time, so a replacement could
// leave candidate B's existence beside candidate A's digest, an empty
// post-validation capture left a stale certified PRESENT, and a failed
// recapture kept a digest the worktree no longer held. Every transition now
// builds a complete observation and replaces the current one whole
// (replaceCurrentCandidate), or enriches it only with a fact mechanically shown
// to describe the same content. The state and every identity field of a
// receipt are read from one value of this type.
type candidateObservation struct {
	// candidateState is a STATE, not a boolean. An earlier draft used
	// `candidateExists bool`, which reintroduced exactly the ambiguity removed
	// from Attempt.Delivered: false conflated "measured: no candidate" with
	// "nobody recorded anything". A run's record opens at NONE -- a positive
	// claim that nothing has been created yet -- and a task with no open
	// record reads UNKNOWN.
	candidateState                             runreceipt.CandidateState
	candCommit, candTree, candParent, candDiff runreceipt.Value
	// capturedTree is the content identity the certified capture froze.
	capturedTree runreceipt.Value
	// candRendering is the canonical rendering of the MINTED object, and
	// digestRelation is how it compares with the rendering the review saw.
	candRendering  runreceipt.Value
	digestRelation runreceipt.DigestRelation
	// certified records that THIS candidate was certified against its frozen
	// capture in this run, and candBase the base it was cut from. No later
	// failure or terminal reason un-establishes it (DF-35); only another
	// observation of the current candidate replaces it.
	certified bool
	candBase  string
	// unmeasured, when set, is why the current candidate's content could not
	// be measured. No ref read at the terminal can then be shown to describe
	// that content, so none is merged into it.
	unmeasured string
}

// candidateWith is an observation in state whose every identity field is
// unmeasured for the one reason why.
func candidateWith(state runreceipt.CandidateState, why string) candidateObservation {
	v := runreceipt.UnknownValue("not measured: " + why)
	return candidateObservation{
		candidateState: state, candCommit: v, candTree: v, candParent: v, candDiff: v,
		capturedTree: v, candRendering: v,
		// UNKNOWN until something measures it, and UNKNOWN is never
		// sufficient for a complete record of a candidate that exists.
		digestRelation: runreceipt.RelationUnknown,
	}
}

// openingCandidate is the observation before anything is measured: NONE, with
// the canonical true-absence representation in every field.
func openingCandidate() candidateObservation {
	o := candidateWith(runreceipt.CandidateNone, "no candidate was created")
	o.capturedTree = runreceipt.UnknownValue("not measured: no candidate was captured")
	o.candRendering = runreceipt.UnknownValue("not measured: no candidate identity was minted")
	return o
}

// emptyCandidate is a capture that measured no change from the base: there is
// no current candidate, whatever existed before it.
func emptyCandidate(tree string) candidateObservation {
	return candidateWith(runreceipt.CandidateNone,
		"no current candidate: the capture froze tree "+tree+", the base tree, so it holds no change")
}

// certifiedCandidate is the complete observation one certified capture
// establishes. It has no minted identity; the terminal reads that from the
// candidate ref, if the ref is shown to hold this content.
func certifiedCandidate(base, digest, tree string) candidateObservation {
	o := candidateWith(runreceipt.CandidatePresent, "the certified candidate (tree "+tree+") has no minted identity in this run")
	o.candDiff = runreceipt.MeasuredValue(digest, candidateDigestSource)
	o.capturedTree = runreceipt.MeasuredValue(tree, "the canonical tree the capture froze")
	o.certified, o.candBase = true, strings.TrimSpace(base)
	return o
}

// contentChanged is the observation once a measurement shows its content is
// not what it described: existence stands, and every identity measured of the
// earlier content goes with it.
func (o candidateObservation) contentChanged(why string) candidateObservation {
	next := candidateWith(o.candidateState, why)
	next.candBase = o.candBase
	return next
}

// established reports whether this observation holds a candidate no terminal
// may deny: one certified against its capture, or one minted.
func (o candidateObservation) established() bool {
	return o.certified || o.candCommit.State == runreceipt.Known
}

// replaceCurrentCandidate makes next the current candidate observation, whole.
// It is the one assignment every candidate transition ends in, so no field of
// the previous observation can outlive it.
func (f *receiptFacts) replaceCurrentCandidate(next candidateObservation) {
	f.candidateObservation = next
}

// candidateRef is what the terminal read from the candidate branch ref, each
// measurement with its own failure, so one unreadable fact cannot take the
// others with it or be reported as an absence.
type candidateRef struct {
	ref                             string
	commit, tree, parent, rendering string
	readErr, treeErr, parentErr     error
	renderErr                       error
}

// readCandidateRef measures the candidate branch ref: the commit it names, that
// commit's tree and first parent, and sha256 of the canonical rendering of
// base..commit. It reads Git objects only -- never the mutable worktree and
// never anything a worker said.
//
// It is indirected so a test can make one measurement fail. Production never
// replaces it.
var readCandidateRef = func(ctx context.Context, repo gitx.Repo, branch, base string) candidateRef {
	r := candidateRef{ref: "refs/heads/" + branch}
	if strings.TrimSpace(repo.Root) == "" {
		r.readErr = errors.New("no repository is configured for this engine")
		return r
	}
	commits, err := repo.RevList(ctx, r.ref, 1)
	switch {
	case err != nil:
		r.readErr = err
		return r
	case len(commits) == 0:
		r.readErr = errors.New("the ref names no commit")
		return r
	}
	r.commit = commits[0]
	r.tree, r.treeErr = repo.CommitTreeOf(ctx, r.commit)
	r.parent, r.parentErr = repo.FirstParentOf(ctx, r.commit)
	if r.parentErr == nil && r.parent == "" {
		r.parentErr = errors.New("the commit is a root commit and has no first parent")
	}
	if base == "" {
		r.renderErr = errors.New("the candidate's base is not recorded, so base..ref cannot be rendered")
		return r
	}
	rendered, err := repo.RenderCandidateDiff(ctx, base, r.commit)
	if err != nil {
		r.renderErr = err
		return r
	}
	r.rendering = candidateRevision(rendered)
	return r
}

// terminalCandidate projects the current candidate observation, once, at the
// terminal, and reports where the call site's claimed state disagrees with it.
//
//   - Existence is the observation's, and only the observation's. An
//     observation that established a candidate -- certified or minted -- is
//     stated PRESENT whatever the terminal; a terminal reason may explain why
//     execution ended and may not change what candidate existed. The state a
//     call site passes is compared and never written: a disagreement is
//     returned as a diagnostic and the projection is unchanged by it.
//   - A minted identity (the accepted path) is stated exactly as the mint
//     measured it. Nothing here re-measures or rewrites it.
//   - A PRESENT or UNATTEMPTED candidate whose content was measured is
//     enriched from the candidate ref only when the ref is mechanically shown
//     to hold that content, and every ref measurement that fails is UNKNOWN
//     with its exact failure, never an absence claim. A candidate whose
//     content could not be measured is stated with that failure and nothing
//     from the ref.
//
// CandidateCommitDiffDigest stays the rendering of a MINTED object only.
func (e *Engine) terminalCandidate(taskID string, f receiptFacts, claimed runreceipt.CandidateState) (candidateObservation, string) {
	o := f.candidateObservation
	if o.established() {
		o.candidateState = runreceipt.CandidatePresent
	}
	var disagreement string
	if claimed != o.candidateState {
		disagreement = "the terminal call site claimed candidate_state " + string(claimed) +
			"; the current candidate observation is " + string(o.candidateState) + ", and only the observation is projected"
	}
	if o.candCommit.State == runreceipt.Known {
		return o, disagreement
	}
	if o.candidateState != runreceipt.CandidatePresent && o.candidateState != runreceipt.CandidateUnattempted {
		return o, disagreement
	}
	if o.unmeasured != "" {
		return o, disagreement
	}

	base := o.candBase
	if base == "" && f.base.State == runreceipt.Known {
		base = f.base.Text
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ref := readCandidateRef(ctx, e.Repo, e.Repo.WorktreeBranch(taskID), base)
	o.candCommit, o.candTree, o.candParent, o.candDiff = identityFromRef(ref, base, o.candDiff, o.capturedTree)

	if o.candRendering.State != runreceipt.Known {
		why := "not measured: the candidate exists and this run minted no canonical " +
			"identity for it; this digest is taken only of the rendering of a minted object"
		// The ref is named as the candidate's source only when it was shown
		// to hold the candidate; a ref holding something else is not this
		// candidate's provenance.
		if o.candCommit.State == runreceipt.Known {
			why += "; the candidate ref " + ref.ref + " names commit " + ref.commit + ", which this run did not mint"
			if ref.renderErr != nil {
				why += ", and whose diff from the base could not be rendered: " + ref.renderErr.Error()
			}
		}
		o.candRendering = runreceipt.UnknownValue(why)
	}
	if o.candidateState == runreceipt.CandidateUnattempted {
		// UNATTEMPTED states that no canonical identity was created, so what
		// the ref holds is reported as observed, not stated as that identity.
		for _, v := range []*runreceipt.Value{&o.candCommit, &o.candTree, &o.candParent} {
			if v.State == runreceipt.Known {
				*v = runreceipt.UnknownValue("not stated as identity: candidate_state UNATTEMPTED records that the run " +
					"refused before creating a canonical identity; the candidate ref holds " + v.Text + " (" + v.Source + ")")
			}
		}
	}
	return o, disagreement
}

// identityFromRef turns a ref reading into commit, tree, first parent and diff
// digest for a candidate that exists.
//
// The ref is the candidate's identity only when it is mechanically shown to
// hold the current candidate's measured content: base..ref must render to the
// validated digest, and the ref's tree must be the captured tree when both are
// measured; a rendering that fails refuses the ref only when its tree is not
// shown to be the captured tree, since tree equality alone establishes the
// content and the certified digest stands. With no validated digest, the ref's tree must equal the captured
// tree; its rendering is then the digest, and a rendering that fails leaves
// only the digest UNKNOWN. A current candidate with no measured content -- an
// inherited or unfrozen worktree -- is shown equal to no ref, so a ref naming
// an older candidate is never stated as its identity. Two observations not
// shown to be one candidate are never merged.
func identityFromRef(r candidateRef, base string, validated, captured runreceipt.Value) (commit, tree, parent, diff runreceipt.Value) {
	unknown := func(why string) runreceipt.Value { return runreceipt.UnknownValue(why) }
	diff = validated
	if r.readErr != nil {
		why := "candidate ref " + r.ref + " could not be read: " + r.readErr.Error()
		if validated.State != runreceipt.Known {
			diff = unknown(why)
		}
		return unknown(why), unknown(why), unknown(why), diff
	}
	if base != "" && r.commit == base {
		why := "candidate ref " + r.ref + " still names the base " + base +
			": the candidate is not committed on it, so the ref measures no commit, tree or first parent of it"
		if validated.State != runreceipt.Known {
			diff = unknown(why)
		}
		return unknown(why), unknown(why), unknown(why), diff
	}
	if validated.State != runreceipt.Known {
		why := ""
		switch {
		// The reasons name the current candidate and never the ref's own
		// objects: what the ref holds is not shown to be this candidate, so
		// none of it is written into this candidate's record.
		case captured.State != runreceipt.Known:
			why = "candidate ref " + r.ref + " is not shown to hold the current candidate: its content was not measured, " +
				"so no ref commit can be shown equal to it"
		case r.treeErr != nil:
			why = "candidate ref " + r.ref + " is not shown to hold the captured candidate tree " + captured.Text +
				": the ref commit's tree could not be read: " + r.treeErr.Error()
		case r.tree != captured.Text:
			why = "candidate ref " + r.ref + " names a commit whose tree is not the captured candidate tree " + captured.Text
		}
		if why != "" {
			// The digest keeps the observation's own reason: nothing of the
			// ref, its rendering included, is shown to describe this candidate.
			return unknown(why), unknown(why), unknown(why), validated
		}
	}
	// The ref's tree being the captured tree establishes, by itself, that the
	// ref holds the current candidate's content; no rendering is needed for it.
	sameTree := captured.State == runreceipt.Known && r.treeErr == nil && r.tree == captured.Text
	switch {
	case r.renderErr != nil && validated.State == runreceipt.Known && !sameTree:
		why := "candidate ref " + r.ref + " names commit " + r.commit + ", whose diff from the base could not be rendered, " +
			"so it is not shown to hold the validated candidate: " + r.renderErr.Error()
		return unknown(why), unknown(why), unknown(why), diff
	case r.renderErr != nil && validated.State == runreceipt.Known:
		// Shown equal by its tree: commit, tree and first parent stand, and
		// the validated digest is the certified capture's own measurement. The
		// rendering failure belongs only to the rendering the terminal reports.
	case r.renderErr != nil:
		diff = unknown("candidate ref " + r.ref + " names commit " + r.commit + ", whose diff from the base could not be rendered: " + r.renderErr.Error())
	case validated.State == runreceipt.Known && r.rendering != validated.Text:
		why := "candidate ref " + r.ref + " names commit " + r.commit + ", whose diff from the base (sha256 " + r.rendering +
			") is not the validated candidate diff (sha256 " + validated.Text + ")"
		return unknown(why), unknown(why), unknown(why), diff
	case validated.State == runreceipt.Known && captured.State == runreceipt.Known && r.treeErr == nil && r.tree != captured.Text:
		why := "candidate ref " + r.ref + " names commit " + r.commit + ", whose tree " + r.tree +
			" is not the captured candidate tree " + captured.Text
		return unknown(why), unknown(why), unknown(why), diff
	case validated.State != runreceipt.Known:
		diff = runreceipt.MeasuredValue(r.rendering, "sha256 of git diff <base> <candidate ref>, read at the terminal")
	}
	commit = runreceipt.MeasuredValue(r.commit, "git rev-list -n 1 "+r.ref+", read at the terminal")
	tree = runreceipt.MeasuredValue(r.tree, "git rev-parse <candidate ref commit>^{tree}")
	if r.treeErr != nil {
		tree = unknown("the tree of candidate ref commit " + r.commit + " could not be read: " + r.treeErr.Error())
	}
	parent = runreceipt.MeasuredValue(r.parent, "git rev-parse <candidate ref commit>^1")
	if r.parentErr != nil {
		parent = unknown("the first parent of candidate ref commit " + r.commit + " could not be read: " + r.parentErr.Error())
	}
	return commit, tree, parent, diff
}

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
	base, graph, plan                          runreceipt.Value
	candCommit, candTree, candParent, candDiff runreceipt.Value
	// capturedTree and reviewedTree are DIFFERENT FACTS and were one field.
	//
	// The capture freezes a tree before anything judges it; a reviewed tree
	// exists only once a bounded verdict comes back bound to one. Writing the
	// capture into a field named "reviewed" made the mint use a remembered
	// PRE-review measurement while claiming it used the reviewed one.
	capturedTree runreceipt.Value
	reviewedTree runreceipt.Value
	// candRendering is the canonical rendering of the MINTED object, and
	// digestRelation is how it compares with the rendering the review saw.
	candRendering  runreceipt.Value
	digestRelation runreceipt.DigestRelation
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
	// candidateState and planState are STATES, not booleans.
	//
	// An earlier draft used `candidateExists bool`, which reintroduced exactly
	// the ambiguity just removed from Attempt.Delivered: false conflated
	// "measured: no candidate" with "nobody recorded anything". A run's record
	// opens at NONE -- a positive claim that nothing has been created yet --
	// and a task with no open record reads UNKNOWN.
	candidateState runreceipt.CandidateState
	planState      runreceipt.PlanState
	// certified records that a validated candidate was certified against its
	// frozen capture in this run, and candBase the base it was cut from. Once
	// set it is never cleared: a later failure cannot un-establish a candidate
	// the run has already validated (DF-35).
	certified bool
	candBase  string
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
		base:             notYet("the run did not reach the start gate"),
		graph:            notYet("the run did not reach the start gate"),
		plan:             notYet("no plan was established for this run"),
		candCommit:       notYet("no candidate was created"),
		candTree:         notYet("no candidate was created"),
		candParent:       notYet("no candidate was created"),
		candDiff:         notYet("no candidate was created"),
		capturedTree:     notYet("no candidate was captured"),
		candRendering:    notYet("no candidate identity was minted"),
		deferredQuestion: notYet("no authority question was deferred"),
		executionBudget:  notYet("no execution budget expired"),
		externalBlock:    notYet("no role turn was blocked externally"),
		notConverged:     notYet("the run did not end unconverged"),
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
		// The relation is UNKNOWN until something measures it, and UNKNOWN is
		// never sufficient for a complete record of a candidate that exists.
		digestRelation: runreceipt.RelationUnknown,
		reviewedTree:   notYet("no bounded review was delivered"),
		provider:       notYet("no reviewer was assigned"),
		executable:     notYet("the engine does not measure the reviewer executable"),
		verdict:        notYet("no bounded verdict was returned"),
		digest:         notYet("no bounded verdict was returned"),
		serving:        notYet("the awareness process had not been launched"),
		// Opening states. Nothing has been CREATED yet, so the candidate axis
		// opens at NONE as a positive claim. The plan axis does NOT: a supplied
		// plan may already exist before the first step succeeds, and a resumed
		// task with no restored record certainly cannot claim there is no plan.
		// Opening at NONE would have let an early failure deny a plan that
		// exists. It opens UNKNOWN and is asserted by whoever establishes it.
		candidateState: runreceipt.CandidateNone,
		planState:      runreceipt.PlanUnknown,
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

// noteCandidateDigest records the identity of the candidate's content.
//
// The source names where the digest is measured: the certified capture, which
// noteCertifiedCandidate records before any inspection can end the run. It must
// not name the review binding, which a run refused before review never builds.
func (e *Engine) noteCandidateDigest(taskID, digest string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		f.candDiff = runreceipt.MeasuredValue(digest, candidateDigestSource)
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
func (e *Engine) noteCertifiedCandidate(taskID, base, digest, tree, baseTree string) {
	e.noteCandidateDigest(taskID, digest)
	e.noteCapturedTree(taskID, tree)
	e.noteCandidateWork(taskID, tree, baseTree)
	e.withReceipt(taskID, func(f *receiptFacts) {
		if f.candidateState == runreceipt.CandidatePresent {
			f.certified = true
			f.candBase = strings.TrimSpace(base)
		}
	})
}

// noteCapturedTree records the content identity the capture froze.
//
// This is NOT the reviewed tree. Nothing has judged it yet, and a field that
// conflated the two let the mint use a pre-review measurement while reporting
// it as the reviewed one.
func (e *Engine) noteCapturedTree(taskID, tree string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		f.capturedTree = runreceipt.MeasuredValue(tree, "the canonical tree the capture froze")
	})
}

// noteCandidateCommit records the accepted candidate's Git identity.
//
// It was called nowhere when the receipt first shipped, and that absence WAS
// F1: the loop left its candidate uncommitted, so a PRESENT candidate could
// never state its commit, tree or first parent and the main success path could
// not produce a complete account of itself. mintCandidateIdentity calls it now.
func (e *Engine) noteCandidateCommit(taskID, commit, tree, firstParent string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		f.candCommit = runreceipt.MeasuredValue(commit, "git rev-parse on the candidate ref")
		f.candTree = runreceipt.MeasuredValue(tree, "git rev-parse <candidate>^{tree}")
		f.candParent = runreceipt.MeasuredValue(firstParent, "git rev-parse <candidate>^1")
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
func (e *Engine) emitReceipt(taskID string, terminal event.Kind, outcome runreceipt.Outcome, candState runreceipt.CandidateState) runreceipt.Receipt {
	e.mu.Lock()
	f := e.receipts[taskID]
	if f == nil {
		// A terminal reached without an open record still emits one, and it
		// says so field by field rather than carrying invalid blanks.
		f = freshFacts()
	}
	facts := *f
	e.mu.Unlock()

	// Every candidate field below is read from this ONE observation, so the
	// state and the identity fields cannot disagree about whether a candidate
	// exists.
	cand := e.terminalCandidate(taskID, facts, candState)
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
		CandidateCommitDiffDigest: cand.rendering,
		CandidateDigestRelation:   cand.relation,
		CandidateState:            cand.state,
		CandidateCommit:           cand.commit,
		CandidateTree:             cand.tree,
		CandidateFirstParent:      cand.parent,
		CandidateDigest:           cand.diff,
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
	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.RunReceipt,
		"governed run receipt: "+string(state)+" / "+string(outcome),
		map[string]any{"receipt": r, "completeness": string(state), "missing": missing}))
	return r
}

// noteCandidateWork records whether the candidate holds WORK, measured.
//
// A worktree is an execution container, not a candidate: firing PRESENT when
// the directory was created made a run that produced nothing owe a commit, and
// minting an empty one to satisfy that would be a fabricated specimen in Git
// clothing. PRESENT and NONE are both read from the frozen tree.
func (e *Engine) noteCandidateWork(taskID, tree, baseTree string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		if tree != "" && tree == baseTree {
			f.candidateState = runreceipt.CandidateNone
			return
		}
		f.candidateState = runreceipt.CandidatePresent
	})
}

// noteCandidateWorkUnmeasured says the content is moving and unmeasured.
//
// A worker is editing, and a stale NONE here would deny work that exists.
func (e *Engine) noteCandidateWorkUnmeasured(taskID string) {
	e.withReceipt(taskID, func(f *receiptFacts) { f.candidateState = runreceipt.CandidateUnknown })
}

// noteInheritedCandidate records what a resumed invocation found on disk
// before it did anything: work is PRESENT, a clean worktree is NONE, and a
// worktree that could not be read is UNKNOWN rather than either claim.
func (e *Engine) noteInheritedCandidate(taskID string, seen observation) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		switch {
		case seen.Err != nil:
			f.candidateState = runreceipt.CandidateUnknown
		case seen.DiffBytes > 0 || len(seen.ChangedPaths) > 0:
			f.candidateState = runreceipt.CandidatePresent
		default:
			f.candidateState = runreceipt.CandidateNone
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
// already decided to refuse.
func (e *Engine) noteCandidateUnattempted(taskID string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		f.candidateState = runreceipt.CandidateUnattempted
	})
}

// noteCandidateRendering records the canonical rendering of the MINTED object
// and how it compares with the rendering the review was given.
func (e *Engine) noteCandidateRendering(taskID, digest string, reviewed string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		f.candRendering = runreceipt.MeasuredValue(digest, "sha256 of the canonical rendering of the minted object")
		switch {
		case digest == "" || reviewed == "":
			f.digestRelation = runreceipt.RelationUnknown
		case digest == reviewed:
			f.digestRelation = runreceipt.RelationMatch
		default:
			f.digestRelation = runreceipt.RelationDiffer
		}
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
func (e *Engine) emitRunTerminal(taskID string, kind event.Kind, source event.Source,
	outcome runreceipt.Outcome, cand runreceipt.CandidateState, summary string, payload any) {
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

// candidateObservation is the ONE account of the candidate a receipt states.
//
// DF-35: candidate_state was set from the measured tree while commit, tree and
// first parent were set only by the mint, so every non-accepted terminal after
// a candidate existed said PRESENT beside "no candidate was created". Two
// predicates disagreed about whether a candidate existed. Every candidate field
// of a receipt is now read from this, and this decides existence once.
type candidateObservation struct {
	state                                 runreceipt.CandidateState
	commit, tree, parent, diff, rendering runreceipt.Value
	relation                              runreceipt.DigestRelation
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

// terminalCandidate reconciles what the run recorded with the candidate ref,
// once, at the terminal.
//
//   - A minted identity (the accepted path) is stated exactly as the mint
//     measured it. Nothing here re-measures or rewrites it.
//   - A run that never established a candidate keeps its opening NONE (or the
//     caller's UNKNOWN) and the opening absence reasons, which are then true.
//   - A run that DID establish one -- a certified capture (always PRESENT),
//     or a PRESENT or UNATTEMPTED state -- states it: the ref supplies commit, tree and first
//     parent when it holds the validated candidate, and every measurement that
//     fails is UNKNOWN with its exact failure, never an absence claim.
//
// CandidateCommitDiffDigest stays the rendering of a MINTED object only.
func (e *Engine) terminalCandidate(taskID string, f receiptFacts, state runreceipt.CandidateState) candidateObservation {
	o := candidateObservation{
		state: state, commit: f.candCommit, tree: f.candTree, parent: f.candParent,
		diff: f.candDiff, rendering: f.candRendering, relation: f.digestRelation,
	}
	if f.candCommit.State == runreceipt.Known {
		return o
	}
	// A validated candidate cannot be denied, left unknown, or demoted to
	// "never created" by a caller's state taken while it was moving. Certified
	// dominates every never-minted UNATTEMPTED downgrade (non-convergence,
	// restoration or plan-admission refusal); UNATTEMPTED stays only for work
	// refused before capture certification.
	if f.certified && state != runreceipt.CandidatePresent {
		o.state = runreceipt.CandidatePresent
	}
	if o.state != runreceipt.CandidatePresent && o.state != runreceipt.CandidateUnattempted {
		return o
	}

	base := f.candBase
	if base == "" && f.base.State == runreceipt.Known {
		base = f.base.Text
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ref := readCandidateRef(ctx, e.Repo, e.Repo.WorktreeBranch(taskID), base)
	o.commit, o.tree, o.parent, o.diff = identityFromRef(ref, base, f.candDiff)

	if o.rendering.State != runreceipt.Known {
		o.rendering = runreceipt.UnknownValue("not measured: the candidate exists and this run minted no canonical " +
			"identity for it; this digest is taken only of the rendering of a minted object")
	}
	if o.state == runreceipt.CandidateUnattempted {
		// UNATTEMPTED states that no canonical identity was created, so what
		// the ref holds is reported as observed, not stated as that identity.
		for _, v := range []*runreceipt.Value{&o.commit, &o.tree, &o.parent} {
			if v.State == runreceipt.Known {
				*v = runreceipt.UnknownValue("not stated as identity: candidate_state UNATTEMPTED records that the run " +
					"refused before creating a canonical identity; the candidate ref holds " + v.Text + " (" + v.Source + ")")
			}
		}
	}
	return o
}

// identityFromRef turns a ref reading into commit, tree, first parent and diff
// digest for a candidate that exists.
//
// The ref is the candidate's identity only when it holds the validated
// candidate: base..ref must render to the validated digest. With no validated
// digest recorded in this run, the ref is the only governed source and its
// rendering is the digest.
func identityFromRef(r candidateRef, base string, validated runreceipt.Value) (commit, tree, parent, diff runreceipt.Value) {
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
		why := "candidate ref " + r.ref + " still names the base " + base + ": no commit on it holds the candidate"
		if validated.State != runreceipt.Known {
			diff = unknown(why)
		}
		return unknown(why), unknown(why), unknown(why), diff
	}
	if r.renderErr != nil {
		why := "candidate ref " + r.ref + " names commit " + r.commit + ", whose diff from the base could not be rendered: " + r.renderErr.Error()
		if validated.State != runreceipt.Known {
			diff = unknown(why)
		}
		return unknown(why), unknown(why), unknown(why), diff
	}
	if validated.State == runreceipt.Known {
		if r.rendering != validated.Text {
			why := "candidate ref " + r.ref + " names commit " + r.commit + ", whose diff from the base (sha256 " + r.rendering +
				") is not the validated candidate diff (sha256 " + validated.Text + ")"
			return unknown(why), unknown(why), unknown(why), diff
		}
	} else {
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

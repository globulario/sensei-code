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
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/globulario/sensei-code/internal/event"
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
	// deferredQuestion is the authority question a run left standing.
	deferredQuestion                      runreceipt.Value
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
// A supplied plan arrives with its own digest. An architect's plan had none at
// all until this receipt asked for one: the bound that governs a run is an
// artifact, and an artifact a run cannot name is one no later reader can check
// a candidate against. The Source distinguishes the two rather than one field
// silently meaning two things.
func (e *Engine) notePlan(taskID, suppliedDigest, planText string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		if strings.TrimSpace(suppliedDigest) != "" {
			f.planState = runreceipt.PlanPresent
			f.plan = runreceipt.MeasuredValue(suppliedDigest, "sha256 of the supplied plan, as handed in")
			return
		}
		if strings.TrimSpace(planText) == "" {
			// A conversational answer carries no plan. NONE is the claim, and
			// the digest is the recorded absence that claim requires.
			f.planState = runreceipt.PlanNone
			f.plan = runreceipt.UnknownValue("no plan text was produced for this run")
			return
		}
		f.planState = runreceipt.PlanPresent
		sum := sha256.Sum256([]byte(planText))
		f.plan = runreceipt.MeasuredValue(hex.EncodeToString(sum[:]), "sha256 of the architect's plan text")
	})
}

// noteCandidateDigest records the identity of the candidate's content.
func (e *Engine) noteCandidateDigest(taskID, digest string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		f.candDiff = runreceipt.MeasuredValue(digest, "sha256 of the candidate diff, as the review binding names it")
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
func (e *Engine) emitReceipt(taskID string, terminal event.Kind, outcome runreceipt.Outcome, cand runreceipt.CandidateState) runreceipt.Receipt {
	e.mu.Lock()
	f := e.receipts[taskID]
	if f == nil {
		// A terminal reached without an open record still emits one, and it
		// says so field by field rather than carrying invalid blanks.
		f = freshFacts()
	}
	facts := *f
	e.mu.Unlock()

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
		CandidateCommitDiffDigest: facts.candRendering,
		CandidateDigestRelation:   facts.digestRelation,
		CandidateState:            cand,
		CandidateCommit:           facts.candCommit,
		CandidateTree:             facts.candTree,
		CandidateFirstParent:      facts.candParent,
		CandidateDigest:           facts.candDiff,
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

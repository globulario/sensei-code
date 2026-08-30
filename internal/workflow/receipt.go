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
	"os/exec"
	"runtime/debug"
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
	provider, executable, verdict, digest      runreceipt.Value
	serving                                    runreceipt.Value
	attempts                                   []runreceipt.Attempt
	// candidateExists is measured when the worktree is created.
	candidateExists bool
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
		base:       notYet("the run did not reach the start gate"),
		graph:      notYet("the run did not reach the start gate"),
		plan:       notYet("no plan was established for this run"),
		candCommit: notYet("no candidate was created"),
		candTree:   notYet("no candidate was created"),
		candParent: notYet("no candidate was created"),
		candDiff:   notYet("no candidate was created"),
		provider:   notYet("no reviewer was assigned"),
		executable: notYet("the engine does not measure the reviewer executable"),
		verdict:    notYet("no bounded verdict was returned"),
		digest:     notYet("no bounded verdict was returned"),
		serving:    notYet("the engine does not measure the awareness process it consults"),
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
func (e *Engine) noteWorld(taskID, base, graphBuildCommit string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		f.base = runreceipt.MeasuredValue(base, "git rev-parse HEAD, at the certified start")
		f.graph = runreceipt.MeasuredValue(graphBuildCommit, "sensei preflight authority.graph_build_commit")
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
			f.plan = runreceipt.MeasuredValue(suppliedDigest, "sha256 of the supplied plan, as handed in")
			return
		}
		if strings.TrimSpace(planText) == "" {
			f.plan = runreceipt.UnknownValue("no plan text was produced for this run")
			return
		}
		sum := sha256.Sum256([]byte(planText))
		f.plan = runreceipt.MeasuredValue(hex.EncodeToString(sum[:]), "sha256 of the architect's plan text")
	})
}

// noteCandidateDigest records the identity of the candidate's content.
func (e *Engine) noteCandidateDigest(taskID, digest string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		f.candDiff = runreceipt.MeasuredValue(digest, "sha256 of the candidate diff, truncated as the review binding does")
	})
}

// noteCandidateCommit records a COMMITTED candidate's Git identity.
//
// It is called nowhere today, and that is the finding rather than an oversight:
// the loop leaves its candidate uncommitted in a worktree, so there is no
// commit, tree or first parent to measure. The receipt says UNKNOWN for all
// three, which makes a PRESENT candidate INCOMPLETE -- exactly the obligation
// C5 was frozen to test, now visible at the production boundary without an
// experiment.
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
		})
	})
}

// noteReviewDelivered records a bounded verdict against the attempt that gave
// it, so the receipt's top-level review and its trail cannot disagree.
func (e *Engine) noteReviewDelivered(taskID, provider, decision, candidateDigest string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		p := runreceipt.MeasuredValue(provider, "the provider recorded in the verdict's provenance")
		v := runreceipt.MeasuredValue(decision, "the reviewer's own decision")
		d := runreceipt.MeasuredValue(candidateDigest, "the candidate digest the verdict names")
		f.provider, f.verdict, f.digest = p, v, d
		if n := len(f.attempts); n > 0 {
			f.attempts[n-1].Provider = p
			f.attempts[n-1].Delivery = runreceipt.DeliveryValue(runreceipt.Delivered, "the verdict this attempt returned")
			f.attempts[n-1].Verdict = v
			f.attempts[n-1].Digest = d
			return
		}
		f.attempts = append(f.attempts, runreceipt.Attempt{
			Provider: p,
			Delivery: runreceipt.DeliveryValue(runreceipt.Delivered, "the verdict this attempt returned"),
			Verdict:  v, Digest: d,
		})
	})
}

// emitReceipt is the terminal boundary: one receipt, at the end of one run.
//
// Outcome and CandidateState are parameters rather than derived state, so a new
// terminal path must decide both. Deriving them here would be the convenience
// that lets the next author skip the question, and the question is the point.
func (e *Engine) emitReceipt(taskID string, outcome runreceipt.Outcome, cand runreceipt.CandidateState) runreceipt.Receipt {
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
		Schema:               runreceipt.SchemaVersion,
		GovernorCommit:       governor,
		GovernorBinarySHA256: binary,
		BaseCommit:           facts.base,
		PlanDigest:           facts.plan,
		GraphDigest:          facts.graph,
		CandidateState:       cand,
		CandidateCommit:      facts.candCommit,
		CandidateTree:        facts.candTree,
		CandidateFirstParent: facts.candParent,
		CandidateDigest:      facts.candDiff,
		ServingProducer:      facts.serving,
		ReviewerProvider:     facts.provider,
		ReviewerExecutable:   facts.executable,
		ReviewVerdict:        facts.verdict,
		ReviewedDigest:       facts.digest,
		Attempts:             facts.attempts,
		Terminal:             runreceipt.MeasuredValue(string(outcome), "the terminal path this run took"),
		Outcome:              outcome,
	}
	state, missing := r.Completeness()
	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.RunReceipt,
		"governed run receipt: "+string(state)+" / "+string(outcome),
		map[string]any{"receipt": r, "completeness": string(state), "missing": missing}))
	return r
}

// noteCandidateCreated records that a candidate worktree now exists.
//
// It is a measurement, not a guess: the engine created the worktree, so it
// knows. Terminal paths that cannot see the candidate from where they stand --
// the generic failure closure, above all -- read this rather than assuming.
func (e *Engine) noteCandidateCreated(taskID string) {
	e.withReceipt(taskID, func(f *receiptFacts) { f.candidateExists = true })
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
	if f.candidateExists {
		return runreceipt.CandidatePresent
	}
	return runreceipt.CandidateNone
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
func (e *Engine) noteAwarenessProducer(taskID, command string) {
	e.withReceipt(taskID, func(f *receiptFacts) {
		if strings.TrimSpace(command) == "" {
			f.serving = runreceipt.UnknownValue("no awareness command is configured")
			return
		}
		f.serving = fileDigest(
			func() (string, error) { return exec.LookPath(command) },
			"sha256 of the executable this run launched for awareness")
	})
}

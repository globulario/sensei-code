package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/runreceipt"
)

// DF-41A4 (RULING-149): AN IMPLEMENTER_INCOMPLETE TERMINAL REPORTS THE
// CANDIDATE THAT ACTUALLY EXISTS. The production witnesses drive the landed
// 70A3 retry bound through the real candidate loop (the completion rig of
// implementer_incomplete_test.go) and end through the production terminal, so
// the receipt read here is the one the run emits. Only the validation commands
// vary, to commit the candidate on its ref, empty it, or break its recapture.

const incompleteTask = "Rewrite main.go so it prints a number."

// absenceLanguage is every phrase a receipt field uses to deny a candidate.
var absenceLanguage = []string{
	"no candidate was created", "no candidate identity was minted", "no candidate was captured",
	"no current candidate", "because no candidate",
}

// candidateFields are the receipt's candidate identity fields.
func candidateFields(r runreceipt.Receipt) map[string]runreceipt.Value {
	return map[string]runreceipt.Value{
		"candidate_commit": r.CandidateCommit, "candidate_tree": r.CandidateTree,
		"candidate_first_parent": r.CandidateFirstParent, "candidate_digest": r.CandidateDigest,
		"candidate_commit_diff_digest": r.CandidateCommitDiffDigest,
	}
}

// noAbsence fails if any candidate field of a PRESENT receipt denies it.
func noAbsence(t *testing.T, r runreceipt.Receipt) {
	t.Helper()
	for name, v := range candidateFields(r) {
		for _, absence := range absenceLanguage {
			if strings.Contains(v.Detail, absence) {
				t.Errorf("%s says %q beside candidate_state %s: %+v", name, absence, r.CandidateState, v)
			}
		}
	}
}

// mentions fails if any candidate field names one of the forbidden identities.
func mentions(t *testing.T, r runreceipt.Receipt, what string, ids ...string) {
	t.Helper()
	for name, v := range candidateFields(r) {
		for _, id := range ids {
			if id != "" && (v.Text == id || strings.Contains(v.Detail, id) || strings.Contains(v.Source, id)) {
				t.Errorf("%s names %s %s: %+v", name, what, id, v)
			}
		}
	}
}

// committingValidation formats main.go and then commits it on the candidate
// branch, so the candidate ref holds exactly the validated candidate.
func committingValidation() config.Validation {
	v := formattingValidation()
	v.Format = append(v.Format, config.Command{Command: "sh", Args: []string{"-c",
		"git add main.go && git -c user.name=w -c user.email=w@w commit -q -m candidate >/dev/null 2>&1 || true"}})
	return v
}

// whenPrinting runs script in the worktree only once main.go prints value, so
// one attempt of the sequence is affected and the earlier ones are not.
func whenPrinting(value int, script string) config.Command {
	return config.Command{Command: "sh", Args: []string{"-c",
		fmt.Sprintf("if grep -q 'println(%d)' main.go; then %s; fi; true", value, script)}}
}

// incompleteTerminal is what the production terminal emitted for one run.
type incompleteTerminal struct {
	err      error
	typed    *ImplementerIncomplete
	payload  ImplementerIncomplete
	receipt  runreceipt.Receipt
	events   []event.Event
	terminal event.Event
}

// endIncomplete runs the rig to its end, requires the landed 70A3 typed state,
// and ends it through the production terminal.
func endIncomplete(t *testing.T, r *completionRig) incompleteTerminal {
	t.Helper()
	_, err := r.run()
	var typed *ImplementerIncomplete
	if !errors.As(err, &typed) {
		t.Fatalf("premise: the run did not end IMPLEMENTER_INCOMPLETE: %v", err)
	}
	return terminate(t, r, err)
}

// terminate ends the run with err through the production terminal.
func terminate(t *testing.T, r *completionRig, err error) incompleteTerminal {
	t.Helper()
	drainEvents(r.h.events)
	r.h.engine.terminateRun(context.Background(), "task-1", incompleteTask, err)
	out := incompleteTerminal{err: err, events: drainEvents(r.h.events)}
	errors.As(err, &out.typed)
	out.receipt = receiptFrom(t, out.events)
	for _, ev := range out.events {
		if _, ok := event.RunTerminality(ev.Kind); ok {
			out.terminal = ev
			if ev.Kind == event.WorkflowFailed && out.typed != nil {
				if jerr := json.Unmarshal(ev.Payload, &out.payload); jerr != nil {
					t.Fatalf("the failed terminal does not carry the typed state: %v", jerr)
				}
			}
		}
	}
	return out
}

// currentCapture measures the worktree as the loop's own recapture does.
func currentCapture(t *testing.T, r *completionRig) gitx.Capture {
	t.Helper()
	c, err := gitx.Repo{Root: r.h.work}.CandidateCapture(context.Background(), r.h.tc.Identity.BaseSHA, r.h.tc.Files)
	if err != nil {
		t.Fatalf("capture the worktree: %v", err)
	}
	return c
}

// branchHistory is every commit on the candidate branch, newest first.
func branchHistory(t *testing.T, r *completionRig) []string {
	t.Helper()
	repo := r.h.engine.Repo
	commits, err := repo.RevList(context.Background(), "refs/heads/"+repo.WorktreeBranch("task-1"), 50)
	if err != nil {
		t.Fatalf("read the candidate branch: %v", err)
	}
	return commits
}

// typedOutcomeOf requires the landed 70A3 payload: cycle 2, three attempts,
// exactly f2 owed, bound to the task and the operative plan attempt.
func typedOutcomeOf(t *testing.T, r *completionRig, x incompleteTerminal) {
	t.Helper()
	if x.terminal.Kind != event.WorkflowFailed {
		t.Fatalf("the terminal is %s, not the supported failed terminal", x.terminal.Kind)
	}
	p := x.payload
	attempt := r.h.engine.operativePlanAttempt("task-1").ID
	if p.State != ImplementerIncompleteState || p.TaskID != "task-1" || p.Cycle != 2 || p.Attempts != 3 ||
		!equalStrings(p.Owed, "f2") || p.PlanAttemptID == "" || p.PlanAttemptID != attempt {
		t.Fatalf("the terminal payload is not the typed IMPLEMENTER_INCOMPLETE state of cycle 2, attempt 3, f2, plan attempt %s: %+v", attempt, p)
	}
	if x.receipt.Outcome != runreceipt.OutcomeFailed || x.receipt.Terminal.Text != string(event.WorkflowFailed) {
		t.Fatalf("the receipt does not bind the failed terminal: outcome %s terminal %+v", x.receipt.Outcome, x.receipt.Terminal)
	}
	if x.receipt.Schema != runreceipt.SchemaVersion || runreceipt.SchemaVersion != "sensei-code.governed-run-receipt/v13" {
		t.Fatalf("schema %q / %q: the candidate vocabulary did not change, so neither may the version", x.receipt.Schema, runreceipt.SchemaVersion)
	}
}

// W1 PRESENT IDENTITY COHERENCE + W10 REAL 70A3 INCOMPLETE TERMINAL. The
// candidate is validated and committed on its ref at every attempt; the landed
// retry bound ends cycle 2 after three incomplete attempts. One terminal carries
// the typed state and a PRESENT candidate whose commit, tree, first parent and
// digest are the current candidate's, with no absence language anywhere.
func TestDF41A4W1W10TheIncompleteTerminalStatesTheCurrentCandidate(t *testing.T) {
	r := exhaustingRig(t, 2)
	r.h.engine.Config.Validation = committingValidation()
	x := endIncomplete(t, r)
	typedOutcomeOf(t, r, x)

	rc := x.receipt
	if rc.CandidateState != runreceipt.CandidatePresent {
		t.Fatalf("candidate_state %s; the validated, committed candidate exists", rc.CandidateState)
	}
	assertRefIdentity(t, r.h, rc)
	assertCertifiedDigestSource(t, rc)
	now := currentCapture(t, r)
	if rc.CandidateDigest.Text != candidateRevision(now.Diff) || rc.CandidateTree.Text != now.Tree {
		t.Fatalf("the receipt does not describe the current candidate: digest %s tree %s, current %s %s",
			rc.CandidateDigest.Text, rc.CandidateTree.Text, candidateRevision(now.Diff), now.Tree)
	}
	if !strings.Contains(now.Diff, "println(4)") {
		t.Fatalf("premise: the current candidate is not the third attempt's: %s", now.Diff)
	}
	noAbsence(t, rc)
	// Incomplete attempts are never reviewed, so the current candidate is not
	// the tree cycle 1's verdict was bound to, and the receipt says so.
	_, missing := rc.Completeness()
	for _, m := range missing {
		if strings.HasPrefix(m, "candidate_") && !mintSpecific(m) && !strings.Contains(m, "is not the reviewed_tree") {
			t.Fatalf("incomplete about more than the mint evidence and the unreviewed movement: %s", m)
		}
	}
}

// W2 PRESENT WITH ONE UNKNOWN FIELD. The ref's first parent is made
// unreadable: PRESENT stands, the commit, tree and digest stay measured, and
// only the first parent is UNKNOWN, with the exact error.
func TestDF41A4W2OneUnmeasurableFieldIsTheOnlyUnknown(t *testing.T) {
	r := exhaustingRig(t, 2)
	r.h.engine.Config.Validation = committingValidation()
	restore := readCandidateRef
	injected := errors.New("first parent unreadable (injected by the DF-41A4 W2 witness)")
	readCandidateRef = func(ctx context.Context, repo gitx.Repo, branch, base string) candidateRef {
		ref := restore(ctx, repo, branch, base)
		ref.parent, ref.parentErr = "", injected
		return ref
	}
	defer func() { readCandidateRef = restore }()

	rc := endIncomplete(t, r).receipt
	if rc.CandidateState != runreceipt.CandidatePresent {
		t.Fatalf("candidate_state %s", rc.CandidateState)
	}
	if rc.CandidateFirstParent.State != runreceipt.Unknown || !strings.Contains(rc.CandidateFirstParent.Detail, injected.Error()) {
		t.Fatalf("candidate_first_parent = %+v, want UNKNOWN with the exact error", rc.CandidateFirstParent)
	}
	for name, v := range map[string]runreceipt.Value{
		"candidate_commit": rc.CandidateCommit, "candidate_tree": rc.CandidateTree, "candidate_digest": rc.CandidateDigest,
	} {
		if v.State != runreceipt.Known {
			t.Errorf("one failed measurement took %s with it: %+v", name, v)
		}
	}
	noAbsence(t, rc)
}

// W3 TRUE NONE CONTROL. IMPLEMENTER_INCOMPLETE is reached before any canonical
// candidate exists: NONE, the canonical absence in every field, complete about
// its candidate, and another task's candidate is not borrowed.
func TestDF41A4W3NoCandidateIsNone(t *testing.T) {
	r := exhaustingRig(t, 2)
	r.h.engine.beginReceipt("task-2")
	r.h.engine.noteCertifiedCandidate("task-2", r.h.tc.Identity.BaseSHA, "digest-of-another-task", "tree-of-another-task", "base-tree")
	x := terminate(t, r, &ImplementerIncomplete{State: ImplementerIncompleteState, TaskID: "task-1",
		PlanAttemptID: r.h.engine.operativePlanAttempt("task-1").ID, Cycle: 2, Attempts: 3, MaxAttempts: 3, Owed: []string{"f2"}})
	rc := x.receipt
	if x.payload.State != ImplementerIncompleteState || rc.CandidateState != runreceipt.CandidateNone {
		t.Fatalf("payload %+v candidate_state %s, want IMPLEMENTER_INCOMPLETE with NONE", x.payload, rc.CandidateState)
	}
	for name, v := range candidateFields(rc) {
		if v.State != runreceipt.Unknown || !strings.Contains(v.Detail, "no candidate") {
			t.Errorf("%s = %+v, want the recorded absence", name, v)
		}
	}
	mentions(t, rc, "another task's candidate", "digest-of-another-task", "tree-of-another-task")
	if _, missing := rc.Completeness(); strings.Contains(strings.Join(missing, " "), "candidate_") {
		t.Fatalf("a true NONE is incomplete about its candidate: %v", missing)
	}
}

// W4 COMPLETE REPLACEMENT. Every attempt commits a new candidate on the ref;
// the terminal names only the last. No earlier commit, tree or digest appears
// in any candidate field except the replacement's own first parent.
func TestDF41A4W4AReplacedCandidateLeavesNothingBehind(t *testing.T) {
	r := exhaustingRig(t, 2)
	r.h.engine.Config.Validation = committingValidation()
	x := endIncomplete(t, r)
	history := branchHistory(t, r)
	if len(history) < 5 {
		t.Fatalf("premise: four committed candidates on the base, got %v", history)
	}
	ctx := context.Background()
	repo := r.h.engine.Repo
	base := r.h.tc.Identity.BaseSHA
	var stale []string
	for _, c := range history[1:4] {
		tree, err := repo.CommitTreeOf(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		rendered, err := repo.RenderCandidateDiff(ctx, base, c)
		if err != nil {
			t.Fatal(err)
		}
		stale = append(stale, tree, candidateRevision(rendered))
		if c != history[1] {
			stale = append(stale, c)
		}
	}
	rc := x.receipt
	if rc.CandidateCommit.Text != history[0] || rc.CandidateFirstParent.Text != history[1] {
		t.Fatalf("the receipt does not name the replacement: commit %+v parent %+v, branch %v", rc.CandidateCommit, rc.CandidateFirstParent, history)
	}
	mentions(t, rc, "a replaced candidate's identity", stale...)
	if rc.CandidateCommitDiffDigest.State == runreceipt.Known {
		t.Fatalf("a rendering of an unminted candidate is stated: %+v", rc.CandidateCommitDiffDigest)
	}

	// The same law in the observation itself: candidate A minted, then B
	// certified. Nothing of A -- digest, tree, commit, rendering -- survives.
	e := &Engine{}
	e.beginReceipt("task-1")
	e.noteCertifiedCandidate("task-1", "base", "digest-A", "tree-A", "base-tree")
	e.noteCandidateRendering("task-1", "rendering-A", "digest-A")
	e.noteCandidateCommit("task-1", "commit-A", "tree-A", "base")
	e.noteCertifiedCandidate("task-1", "base", "digest-B", "tree-B", "base-tree")
	b := e.emitReceipt("task-1", event.WorkflowFailed, runreceipt.OutcomeFailed, e.candidateStateFor("task-1"))
	if b.CandidateState != runreceipt.CandidatePresent || b.CandidateDigest.Text != "digest-B" || b.CandidateDigestRelation != runreceipt.RelationUnknown {
		t.Fatalf("B is not the current candidate: state %s digest %+v relation %s", b.CandidateState, b.CandidateDigest, b.CandidateDigestRelation)
	}
	mentions(t, b, "candidate A", "digest-A", "tree-A", "commit-A", "rendering-A")
	noAbsence(t, b)
}

// W5 FAILED REPLACEMENT IS COHERENT. Candidate A is certified and minted; B's
// recapture then fails, or B is refused certification. A failed capture
// establishes nothing and proves no equivalence with A, so the observation is
// one coherent UNKNOWN -- never PRESENT with A's identity erased, never NONE --
// whose every field carries the exact failure, or UNATTEMPTED; nothing of A is
// stated beside it.
func TestDF41A4W5AFailedReplacementNeverMixesCandidates(t *testing.T) {
	failure := errors.New("index unreadable (injected by the DF-41A4 W5 witness)")
	reason := "not measured: " + postValidationCapture + " failed: " + failure.Error()
	e := &Engine{}
	e.beginReceipt("task-1")
	e.noteCertifiedCandidate("task-1", "base", "digest-A", "tree-A", "base-tree")
	e.noteCandidateRendering("task-1", "rendering-A", "digest-A")
	e.noteCandidateCommit("task-1", "commit-A", "tree-A", "base")
	e.noteCandidateCaptureFailed("task-1", postValidationCapture, failure)
	f := e.emitReceipt("task-1", event.WorkflowFailed, runreceipt.OutcomeFailed, e.candidateStateFor("task-1"))
	switch f.CandidateState {
	case runreceipt.CandidateUnknown:
	case runreceipt.CandidatePresent:
		t.Fatalf("candidate_state PRESENT after a failed replacement: the attempted capture established nothing, and A's identity is gone")
	default:
		t.Fatalf("candidate_state %s after a failed replacement, want UNKNOWN", f.CandidateState)
	}
	for name, v := range candidateFields(f) {
		if v.State != runreceipt.Unknown || v.Detail != reason {
			t.Errorf("%s = %+v, want UNKNOWN with exactly %q", name, v, reason)
		}
	}
	if f.CandidateDigestRelation != runreceipt.RelationUnknown {
		t.Errorf("candidate_digest_relation %s survived the failed replacement", f.CandidateDigestRelation)
	}
	mentions(t, f, "candidate A", "digest-A", "tree-A", "commit-A", "rendering-A")
	noAbsence(t, f)

	e.beginReceipt("task-2")
	e.noteCertifiedCandidate("task-2", "base", "digest-A", "tree-A", "base-tree")
	e.noteCaptureRefused("task-2", "tree-B", "base-tree")
	u := e.emitReceipt("task-2", event.WorkflowFailed, runreceipt.OutcomeFailed, e.candidateStateFor("task-2"))
	if u.CandidateState != runreceipt.CandidateUnattempted {
		t.Fatalf("candidate_state %s, want UNATTEMPTED for refused work", u.CandidateState)
	}
	mentions(t, u, "candidate A", "digest-A", "tree-A")
	noAbsence(t, u)
}

// W6 EMPTY POST-VALIDATION CAPTURE + W11 NO BORROWED OLD CANDIDATE. Cycle 1's
// candidate was certified and reviewed. On the third cycle-2 attempt validation
// restores main.go to the base, so the post-validation recapture is canonically
// empty: the IMPLEMENTER_INCOMPLETE terminal states NONE and does not resurrect
// any earlier candidate.
func TestDF41A4W6W11AnEmptiedCandidateIsNotStatedPresent(t *testing.T) {
	r := exhaustingRig(t, 2)
	v := formattingValidation()
	v.Format = append(v.Format, whenPrinting(4, "git checkout -q -- main.go"))
	r.h.engine.Config.Validation = v
	x := endIncomplete(t, r)
	typedOutcomeOf(t, r, x)
	if now := currentCapture(t, r); now.Tree != now.BaseTree {
		t.Fatalf("premise: validation did not empty the candidate: %s", now.Diff)
	}
	rc := x.receipt
	if rc.CandidateState != runreceipt.CandidateNone {
		t.Fatalf("candidate_state %s: a candidate certified before validation survived an empty recapture", rc.CandidateState)
	}
	for name, v := range candidateFields(rc) {
		if v.State != runreceipt.Unknown || !strings.Contains(v.Detail, "no current candidate") {
			t.Errorf("%s = %+v, want the empty recapture's recorded absence", name, v)
		}
	}
	if _, missing := rc.Completeness(); strings.Contains(strings.Join(missing, " "), "candidate_") {
		t.Fatalf("NONE after an empty recapture is incomplete about its candidate: %v", missing)
	}

	// W11 in the observation: candidate A of an earlier cycle, then an empty
	// recapture refused certification. A is not the current candidate.
	e := &Engine{}
	e.beginReceipt("task-1")
	e.noteCertifiedCandidate("task-1", "base", "digest-A", "tree-A", "base-tree")
	e.noteCaptureRefused("task-1", "base-tree", "base-tree")
	n := e.emitReceipt("task-1", event.WorkflowFailed, runreceipt.OutcomeFailed, e.candidateStateFor("task-1"))
	if n.CandidateState != runreceipt.CandidateNone {
		t.Fatalf("candidate_state %s: an earlier cycle's candidate was resurrected", n.CandidateState)
	}
	mentions(t, n, "the earlier cycle's candidate", "digest-A", "tree-A")
}

// W7 CAPTURE FAILURE IS NOT ABSENCE. On the third cycle-2 attempt validation's
// formatter corrupts the worktree index, so the recapture after it fails. The
// receipt does not say no candidate exists, and it does not say one does: the
// failed capture established nothing, so the state is UNKNOWN -- not NONE, not
// PRESENT with the earlier candidate's identity erased -- and every candidate
// field is UNKNOWN for the recapture's exact failure, stating no identity of the
// candidate certified before it. The same observation is what an
// IMPLEMENTER_INCOMPLETE terminal then states.
func TestDF41A4W7AFailedRecaptureIsUnknownNotNone(t *testing.T) {
	r := exhaustingRig(t, 2)
	v := formattingValidation()
	v.Format = append(v.Format, whenPrinting(4, `printf garbage > "$(git rev-parse --git-path index)"`))
	r.h.engine.Config.Validation = v
	_, err := r.run()
	var typed *ImplementerIncomplete
	if err == nil || errors.As(err, &typed) || !strings.Contains(err.Error(), "index") {
		t.Fatalf("premise: the recapture after validation's formatter did not fail: %v", err)
	}
	reason := "not measured: " + postFormatCapture + " failed: "
	for _, end := range []error{err, &ImplementerIncomplete{State: ImplementerIncompleteState, TaskID: "task-1",
		PlanAttemptID: r.h.engine.operativePlanAttempt("task-1").ID, Cycle: 2, Attempts: 3, MaxAttempts: 3, Owed: []string{"f2"}}} {
		rc := terminate(t, r, end).receipt
		if rc.CandidateState != runreceipt.CandidateUnknown {
			t.Fatalf("candidate_state %s after a failed recapture, want UNKNOWN: neither NONE nor PRESENT is established", rc.CandidateState)
		}
		var detail string
		for name, v := range candidateFields(rc) {
			if v.State != runreceipt.Unknown || v.Text != "" || !strings.HasPrefix(v.Detail, reason) || !strings.Contains(v.Detail, "index") {
				t.Errorf("%s = %+v, want UNKNOWN with the exact recapture failure and no identity", name, v)
			}
			if detail == "" {
				detail = v.Detail
			} else if v.Detail != detail {
				t.Errorf("%s = %+v, want the one failure %q every other field carries", name, v, detail)
			}
		}
		noAbsence(t, rc)
	}
}

// W8 FROM-REF COMPLETE PROJECTION + W12 STRONGER REF OBSERVATION. The candidate
// is observed through the production candidate-ref read: commit, tree and first
// parent come from the ref and say so, the digest stays the certified capture's
// -- the ref's rendering is mechanically equal to it, so it is one candidate,
// stated once -- and the rendering field names the ref commit as unminted.
func TestDF41A4W8W12TheRefEnrichesTheCertifiedCandidate(t *testing.T) {
	r := exhaustingRig(t, 2)
	r.h.engine.Config.Validation = committingValidation()
	rc := endIncomplete(t, r).receipt
	commit, _, _, refDigest := candidateRefIdentity(t, r.h)
	if rc.CandidateDigest.Text != refDigest || rc.CandidateDigest.Source != candidateDigestSource {
		t.Fatalf("candidate_digest = %+v; want the certified digest, equal to the ref's rendering %s", rc.CandidateDigest, refDigest)
	}
	for name, v := range map[string]runreceipt.Value{
		"candidate_commit": rc.CandidateCommit, "candidate_tree": rc.CandidateTree, "candidate_first_parent": rc.CandidateFirstParent,
	} {
		if v.State != runreceipt.Known || !strings.Contains(v.Source, "candidate ref") && !strings.Contains(v.Source, "refs/heads/"+r.h.engine.Repo.WorktreeBranch("task-1")) {
			t.Errorf("%s = %+v, want a measurement read from the candidate ref", name, v)
		}
	}
	if !strings.Contains(rc.CandidateCommitDiffDigest.Detail, commit) || !strings.Contains(rc.CandidateCommitDiffDigest.Detail, "did not mint") {
		t.Fatalf("candidate_commit_diff_digest does not characterize the ref's provenance: %+v", rc.CandidateCommitDiffDigest)
	}
	noAbsence(t, rc)
}

// W9 FROM-REF PARTIAL MEASUREMENT. The ref resolves, its tree is the captured
// tree -- so it holds the current candidate -- and only one downstream
// measurement fails: its rendering, with no validated digest recorded. Commit,
// tree and first parent stay measured; only the digest is UNKNOWN, exactly.
func TestDF41A4W9AFailedRefRenderingLeavesTheRestMeasured(t *testing.T) {
	failure := errors.New("diff unrenderable (injected by the DF-41A4 W9 witness)")
	none := runreceipt.UnknownValue("not measured")
	captured := runreceipt.MeasuredValue("t1", "the canonical tree the capture froze")
	commit, tree, parent, diff := identityFromRef(candidateRef{ref: "refs/heads/c", commit: "c1", tree: "t1", parent: "base",
		renderErr: failure}, "base", none, captured)
	if commit.Text != "c1" || tree.Text != "t1" || parent.Text != "base" {
		t.Fatalf("one failed measurement erased the others: %+v %+v %+v", commit, tree, parent)
	}
	if diff.State != runreceipt.Unknown || !strings.Contains(diff.Detail, failure.Error()) {
		t.Fatalf("candidate_digest = %+v, want UNKNOWN with the exact rendering failure", diff)
	}
}

// W13 DISAGREEING OBSERVATIONS. A ref whose rendering, or whose tree, is not
// the certified candidate's is not merged into it: commit, tree and first
// parent are UNKNOWN naming the disagreement, and the certified digest stands.
func TestDF41A4W13UnprovenEquivalenceIsNeverMerged(t *testing.T) {
	validated := runreceipt.MeasuredValue("digest-B", candidateDigestSource)
	captured := runreceipt.MeasuredValue("tree-B", "the canonical tree the capture froze")
	for name, ref := range map[string]candidateRef{
		"rendering": {ref: "refs/heads/c", commit: "commit-A", tree: "tree-B", parent: "base", rendering: "digest-A"},
		"tree":      {ref: "refs/heads/c", commit: "commit-A", tree: "tree-A", parent: "base", rendering: "digest-B"},
	} {
		commit, tree, parent, diff := identityFromRef(ref, "base", validated, captured)
		for field, v := range map[string]runreceipt.Value{"commit": commit, "tree": tree, "parent": parent} {
			if v.State != runreceipt.Unknown || !strings.Contains(v.Detail, "is not the") {
				t.Errorf("%s disagreement: %s = %+v, want UNKNOWN naming the disagreement", name, field, v)
			}
		}
		if diff != validated {
			t.Errorf("%s disagreement replaced the certified digest: %+v", name, diff)
		}
	}
}

// W14 OBJECTIVE-62 REGRESSION. The historical shapes -- work measured with no
// identity minted, and a certified candidate the ref cannot describe -- end in
// an IMPLEMENTER_INCOMPLETE terminal. Neither says no candidate was created.
func TestDF41A4W14IncompleteCannotReopenFalseAbsence(t *testing.T) {
	for name, establish := range map[string]func(e *Engine, base string){
		"work":      func(e *Engine, _ string) { e.noteCandidateWork("task-1", "some-tree", "a-different-base-tree") },
		"certified": func(e *Engine, base string) { e.noteCertifiedCandidate("task-1", base, "d", "some-tree", "base-tree") },
	} {
		r := exhaustingRig(t, 2)
		establish(r.h.engine, r.h.tc.Identity.BaseSHA)
		x := terminate(t, r, &ImplementerIncomplete{State: ImplementerIncompleteState, TaskID: "task-1",
			PlanAttemptID: r.h.engine.operativePlanAttempt("task-1").ID, Cycle: 2, Attempts: 3, MaxAttempts: 3, Owed: []string{"f2"}})
		if x.receipt.CandidateState != runreceipt.CandidatePresent || x.payload.State != ImplementerIncompleteState {
			t.Fatalf("%s: candidate_state %s payload %+v", name, x.receipt.CandidateState, x.payload)
		}
		noAbsence(t, x.receipt)
	}

	// A terminal reason explains why execution ended; the state a call site
	// passes cannot change what candidate existed. Against an established or
	// measured candidate, a NONE, UNKNOWN or UNATTEMPTED claim is not projected.
	for name, establish := range map[string]func(e *Engine){
		"work":      func(e *Engine) { e.noteCandidateWork("task-1", "some-tree", "a-different-base-tree") },
		"certified": func(e *Engine) { e.noteCertifiedCandidate("task-1", "base", "d", "some-tree", "base-tree") },
	} {
		for _, claim := range []runreceipt.CandidateState{runreceipt.CandidateNone, runreceipt.CandidateUnknown, runreceipt.CandidateUnattempted} {
			if name == "work" && claim == runreceipt.CandidateUnattempted {
				continue // UNATTEMPTED is the lawful downgrade of never-minted work
			}
			e := &Engine{}
			e.beginReceipt("task-1")
			establish(e)
			rc := e.emitReceipt("task-1", event.WorkflowFailed, runreceipt.OutcomeFailed, claim)
			if rc.CandidateState != runreceipt.CandidatePresent {
				t.Errorf("%s: a terminal claiming %s turned the candidate into %s", name, claim, rc.CandidateState)
			}
			noAbsence(t, rc)
		}
	}
}

// W15 70A3 COMPOSITION + W17 OUTER TERMINAL COMPATIBILITY. The terminal is
// the landed ImplementerIncomplete state carried by the existing failed
// terminal, a task terminal in the closed run-terminal vocabulary; the run
// emits exactly one run terminal and no other incomplete classification.
func TestDF41A4W15W17TheTypedStateRidesTheExistingTerminal(t *testing.T) {
	r := exhaustingRig(t, 2)
	r.h.engine.Config.Validation = committingValidation()
	x := endIncomplete(t, r)
	typedOutcomeOf(t, r, x)
	if x.typed == nil || x.typed.State != ImplementerIncompleteState {
		t.Fatal("the landed typed state was not what ended the run")
	}
	if kind, ok := event.RunTerminality(event.WorkflowFailed); !ok || kind != event.TaskTerminal {
		t.Fatalf("the failed terminal is not a supported run terminal: %s %v", kind, ok)
	}
	terminals := 0
	for _, ev := range x.events {
		if _, ok := event.RunTerminality(ev.Kind); ok {
			terminals++
		}
		if strings.Contains(strings.ToLower(string(ev.Kind)), "incomplete") {
			t.Errorf("a competing incomplete classification was emitted: %s", ev.Kind)
		}
	}
	if terminals != 1 {
		t.Fatalf("%d run terminals were emitted, want exactly one: %v", terminals, kinds(x.events))
	}
	if x.payload.State != x.typed.State || x.payload.Cycle != x.typed.Cycle || x.payload.Attempts != x.typed.Attempts ||
		!equalStrings(x.payload.Owed, x.typed.Owed...) || x.payload.PlanAttemptID != x.typed.PlanAttemptID {
		t.Fatalf("the terminal payload is not the typed state that ended the run: %+v vs %+v", x.payload, *x.typed)
	}
}

// W16 RETRY SEMANTICS UNCHANGED. The same incomplete sequence, once with the
// candidate observation recorded and once with no receipt open at all, spends
// the same invocations in the same cycle, asks for the same findings, retains
// the same ones and exhausts at the same point.
func TestDF41A4W16CandidateTruthDoesNotChangeTheRetry(t *testing.T) {
	type run struct {
		cycles         []int
		reviews, turns int
		owed           []string
		typed          ImplementerIncomplete
	}
	once := func(observed bool) run {
		r := exhaustingRig(t, 2)
		if !observed {
			r.h.engine.mu.Lock()
			delete(r.h.engine.receipts, "task-1")
			r.h.engine.mu.Unlock()
		}
		_, err := r.run()
		var typed *ImplementerIncomplete
		if !errors.As(err, &typed) {
			t.Fatalf("observed=%v: the run did not end IMPLEMENTER_INCOMPLETE: %v", observed, err)
		}
		out := run{cycles: r.cycles(), reviews: r.reviews, turns: r.next, typed: *typed}
		for _, p := range r.prompts {
			out.owed = append(out.owed, owedLines(p))
		}
		return out
	}
	with, without := once(true), once(false)
	if fmt.Sprint(with.cycles) != fmt.Sprint(without.cycles) || with.reviews != without.reviews || with.turns != without.turns ||
		fmt.Sprint(with.owed) != fmt.Sprint(without.owed) {
		t.Fatalf("candidate observation changed the retry: with %+v without %+v", with, without)
	}
	a, b := with.typed, without.typed
	if a.Cycle != b.Cycle || a.Attempts != b.Attempts || fmt.Sprint(a.Owed) != fmt.Sprint(b.Owed) || fmt.Sprint(a.Retained) != fmt.Sprint(b.Retained) {
		t.Fatalf("candidate observation changed the exhaustion: %+v vs %+v", a, b)
	}
	if !equalInts(with.cycles, 1, 2, 2, 2) {
		t.Fatalf("premise: the landed sequence is cycle 1 then three cycle-2 attempts, got %v", with.cycles)
	}
}

// W18 NO DURABILITY CLAIM. After the truthful in-process terminal, a fresh
// engine over the same repository holds no live cycle obligation and no
// candidate observation: 70A4 reconstructs neither. That gap belongs to 70B.
func TestDF41A4W18AFreshEngineReconstructsNothing(t *testing.T) {
	r := exhaustingRig(t, 2)
	r.h.engine.Config.Validation = committingValidation()
	endIncomplete(t, r)
	fresh := &Engine{Repo: r.h.engine.Repo, SessionID: "session-2", Bus: event.NewBus()}
	if c, ok := fresh.liveCycleCompletion("task-1"); ok {
		t.Fatalf("a fresh engine claims the live cycle obligation: %+v", c)
	}
	if got := fresh.candidateStateFor("task-1"); got != runreceipt.CandidateUnknown {
		t.Fatalf("a fresh engine claims a candidate observation it never made: %s", got)
	}
}

// receiptAndDisagreement emits the receipt for a claimed candidate state and
// returns it with the disagreement the RunReceipt event reported, if any.
func receiptAndDisagreement(t *testing.T, e *Engine, claim runreceipt.CandidateState) (runreceipt.Receipt, string) {
	t.Helper()
	events, cancel := e.Bus.Subscribe(64)
	defer cancel()
	rc := e.emitReceipt("task-1", event.WorkflowFailed, runreceipt.OutcomeFailed, claim)
	for _, ev := range drainEvents(events) {
		if ev.Kind != event.RunReceipt {
			continue
		}
		var body struct {
			Disagreement string `json:"candidate_state_disagreement"`
		}
		if err := json.Unmarshal(ev.Payload, &body); err != nil {
			t.Fatalf("the receipt event is unreadable: %v", err)
		}
		return rc, body.Disagreement
	}
	t.Fatal("no receipt event was emitted")
	return rc, ""
}

// staleRefRig ends the landed three-attempt sequence with candidate A -- the
// third attempt's, validated and committed on the candidate ref -- and then
// runs the same task read-only, so its worker writes candidate B into the
// worktree and the read-only refusal returns before anything commits it. The
// ref still names A; the worktree holds uncommitted B.
func staleRefRig(t *testing.T) (r *completionRig, err error, a []string) {
	t.Helper()
	r = exhaustingRig(t, 2)
	r.h.engine.Config.Validation = committingValidation()
	endIncomplete(t, r)
	commit, tree, parent, digest := candidateRefIdentity(t, r.h)
	a = []string{commit, tree, parent, digest}
	r.h.tc.Mode = ModeInspect
	_, err = r.run()
	if err == nil || !strings.Contains(err.Error(), "the plan was read-only and the candidate changed") {
		t.Fatalf("premise: the read-only run did not refuse candidate B early: %v", err)
	}
	if now := currentCapture(t, r); !strings.Contains(now.Diff, "println(5)") {
		t.Fatalf("premise: the worktree does not hold candidate B: %s", now.Diff)
	}
	if head := branchHistory(t, r)[0]; head != commit {
		t.Fatalf("premise: the ref moved from candidate A %s to %s", commit, head)
	}
	return r, err, a
}

// DF-41A4 f1 + f2 EARLY NON-EMPTY REFUSAL. The capture after the worker's turn
// is recorded before the read-only refusal returns, so the terminal states B --
// PRESENT, with B's content and no identity -- and the ref, still naming the
// older candidate A, is not shown to hold B and contributes nothing: no A
// commit, tree, parent or digest enters B's receipt, measured or in a reason.
func TestDF41A4AStaleRefIsNotTheCurrentWorktreeCandidate(t *testing.T) {
	r, err, a := staleRefRig(t)
	for _, end := range []error{err, &ImplementerIncomplete{State: ImplementerIncompleteState, TaskID: "task-1",
		PlanAttemptID: r.h.engine.operativePlanAttempt("task-1").ID, Cycle: 2, Attempts: 3, MaxAttempts: 3, Owed: []string{"f2"}}} {
		rc := terminate(t, r, end).receipt
		if rc.CandidateState != runreceipt.CandidatePresent {
			t.Fatalf("candidate_state %s; the worktree holds candidate B", rc.CandidateState)
		}
		mentions(t, rc, "the stale ref's candidate A", a...)
		for name, v := range candidateFields(rc) {
			if v.State == runreceipt.Known {
				t.Errorf("%s states %+v for a candidate no ref or mint has been shown to hold", name, v)
			}
		}
		noAbsence(t, rc)
	}
}

// DF-41A4 f1. Inherited work, unminted work and refused work in the worktree
// are each a candidate B the stale ref A is not shown to hold, so none of them
// is given A's identity. The control: work whose tree IS the ref's tree is
// mechanically the ref's candidate, and the ref then enriches it.
func TestDF41A4WorktreeObservationsNeverBorrowAStaleRef(t *testing.T) {
	r, _, a := staleRefRig(t)
	e, base := r.h.engine, r.h.tc.Identity.BaseSHA
	b := currentCapture(t, r)
	for name, observe := range map[string]func(){
		"inherited": func() { e.noteInheritedCandidate("task-1", observeCandidate(context.Background(), r.h.work, base)) },
		"unminted":  func() { e.noteCandidateWork("task-1", b.Tree, b.BaseTree) },
		"refused":   func() { e.noteCaptureRefused("task-1", b.Tree, b.BaseTree) },
	} {
		observe()
		rc := terminate(t, r, errors.New("the run ended")).receipt
		if rc.CandidateState != runreceipt.CandidatePresent && rc.CandidateState != runreceipt.CandidateUnattempted {
			t.Fatalf("%s: candidate_state %s; the worktree holds candidate B", name, rc.CandidateState)
		}
		mentions(t, rc, name+" B's receipt naming the stale ref's candidate A", a...)
		for field, v := range candidateFields(rc) {
			if v.State == runreceipt.Known {
				t.Errorf("%s: %s states %+v for a candidate the ref is not shown to hold", name, field, v)
			}
		}
		noAbsence(t, rc)
	}

	// The control: the same content as the ref, so the ref is that candidate.
	// (This rig records no base, so the ref's rendering is UNKNOWN for that
	// exact reason; commit and tree are the ref's.)
	e.noteCandidateWork("task-1", a[1], b.BaseTree)
	rc := terminate(t, r, errors.New("the run ended")).receipt
	if rc.CandidateCommit.Text != a[0] || rc.CandidateTree.Text != a[1] || rc.CandidateFirstParent.Text != a[2] {
		t.Fatalf("work equal to the ref was not enriched from it: commit %+v tree %+v parent %+v",
			rc.CandidateCommit, rc.CandidateTree, rc.CandidateFirstParent)
	}
}

// captureFilter installs, on cycle 1, a required clean filter on main.go that
// performs action whenever main.go prints 4 and passes every other content
// through. The third cycle-2 attempt writes println(4), so it is the capture
// after that attempt's worker turn -- before any validation -- that action
// affects, and the earlier attempts are captured, validated and committed
// unaffected.
func captureFilter(action string) config.Command {
	return whenPrinting(1, `d=$(cd "$(git rev-parse --git-common-dir)" && pwd)
mkdir -p "$d/info"
cat > "$d/df41a4-filter.sh" <<'FILTER'
c=$(cat)
case "$c" in *"println(4)"*) `+action+` ;; *) printf '%s\n' "$c" ;; esac
FILTER
git config filter.df41a4.clean "sh $d/df41a4-filter.sh"
git config filter.df41a4.required true
echo 'main.go filter=df41a4' >> "$d/info/attributes"`)
}

// DF-41A4 f2 INITIAL-CAPTURE OUTCOMES. Candidate A is established by the
// landed sequence -- certified at attempt 2 and committed on the ref -- and the
// capture after attempt 3's worker turn is either canonically empty or fails.
// Either outcome is recorded before its return, so the terminal, the run's own
// error or the typed IMPLEMENTER_INCOMPLETE state, states the current
// candidate: NONE for the empty capture, UNKNOWN with the exact failure for the
// failed one -- which established nothing -- and never candidate A.
func TestDF41A4TheInitialCaptureOutcomeIsRecordedBeforeItsReturn(t *testing.T) {
	for _, c := range []struct {
		name, action, premise string
		state                 runreceipt.CandidateState
		reason                string
	}{
		{"empty", `printf 'package main\n\nfunc main() {}\n'`, "implementor produced no candidate diff",
			runreceipt.CandidateNone, "no current candidate"},
		{"failure", `echo 'df41a4: capture refused' >&2; exit 1`, "df41a4",
			runreceipt.CandidateUnknown, postWorkerCapture + " failed"},
	} {
		r := exhaustingRig(t, 2)
		v := committingValidation()
		v.Format = append(v.Format, captureFilter(c.action))
		r.h.engine.Config.Validation = v
		_, err := r.run()
		var typed *ImplementerIncomplete
		if err == nil || errors.As(err, &typed) || !strings.Contains(err.Error(), c.premise) {
			t.Fatalf("%s: premise: the capture after attempt 3's turn did not end the run: %v", c.name, err)
		}
		if got := r.cycles(); !equalInts(got, 1, 2, 2, 2) {
			t.Fatalf("%s: premise: attempt 3 of cycle 2 was not reached: %v", c.name, got)
		}
		a0, a1, a2, a3 := candidateRefIdentity(t, r.h)
		for _, end := range []error{err, &ImplementerIncomplete{State: ImplementerIncompleteState, TaskID: "task-1",
			PlanAttemptID: r.h.engine.operativePlanAttempt("task-1").ID, Cycle: 2, Attempts: 3, MaxAttempts: 3, Owed: []string{"f2"}}} {
			rc := terminate(t, r, end).receipt
			if rc.CandidateState != c.state {
				t.Fatalf("%s: candidate_state %s, want %s", c.name, rc.CandidateState, c.state)
			}
			for name, v := range candidateFields(rc) {
				if v.State != runreceipt.Unknown || !strings.Contains(v.Detail, c.reason) {
					t.Errorf("%s: %s = %+v, want UNKNOWN for %q", c.name, name, v, c.reason)
				}
			}
			mentions(t, rc, c.name+": the earlier candidate A", a0, a1, a2, a3)
			if c.state != runreceipt.CandidateNone {
				noAbsence(t, rc)
			}
		}
	}
}

// DF-41A4 f3 THE CLAIMED STATE IS A CHECK, NOT A SOURCE. For NONE, UNKNOWN and
// UNATTEMPTED observations -- and a task with no open record -- a terminal
// claiming another state changes nothing projected: the receipt states the
// observation, and the disagreement is reported beside it. A claim that agrees
// reports nothing.
func TestDF41A4TheClaimedCandidateStateNeverWritesTheReceipt(t *testing.T) {
	all := []runreceipt.CandidateState{runreceipt.CandidateNone, runreceipt.CandidatePresent,
		runreceipt.CandidateUnknown, runreceipt.CandidateUnattempted}
	for _, c := range []struct {
		name    string
		observe func(e *Engine)
		want    runreceipt.CandidateState
		reason  string
	}{
		{"none", func(e *Engine) { e.beginReceipt("task-1") }, runreceipt.CandidateNone, "no candidate was created"},
		{"unknown", func(e *Engine) { e.beginReceipt("task-1"); e.noteCandidateWorkUnmeasured("task-1") },
			runreceipt.CandidateUnknown, "has not been captured"},
		{"unattempted", func(e *Engine) {
			e.beginReceipt("task-1")
			e.noteCandidateCaptureFailed("task-1", postValidationCapture, errors.New("unused"))
			e.noteCaptureRefused("task-1", "tree-B", "base-tree")
			// Its content is measured, so the ref is read -- and this engine
			// has none -- rather than the claim being consulted.
		}, runreceipt.CandidateUnattempted, "could not be read"},
		{"unrecorded", func(*Engine) {}, runreceipt.CandidateUnknown, "no receipt record was open"},
	} {
		for _, claim := range all {
			e := &Engine{Bus: event.NewBus()}
			c.observe(e)
			rc, disagreement := receiptAndDisagreement(t, e, claim)
			if rc.CandidateState != c.want {
				t.Errorf("%s: a terminal claiming %s projected %s, want the observation's %s", c.name, claim, rc.CandidateState, c.want)
			}
			if !strings.Contains(rc.CandidateCommit.Detail, c.reason) || rc.CandidateCommit.State != runreceipt.Unknown {
				t.Errorf("%s: claiming %s, candidate_commit = %+v, want the observation's own %q", c.name, claim, rc.CandidateCommit, c.reason)
			}
			if c.want != runreceipt.CandidateNone {
				noAbsence(t, rc)
			}
			switch {
			case claim == c.want && disagreement != "":
				t.Errorf("%s: an agreeing claim reported a disagreement: %s", c.name, disagreement)
			case claim != c.want && (!strings.Contains(disagreement, string(claim)) || !strings.Contains(disagreement, string(c.want))):
				t.Errorf("%s: claiming %s, the disagreement with %s was not reported: %q", c.name, claim, c.want, disagreement)
			}
		}
	}
}

// DF-41A4 cycle 3 f1 AN ERRORED TURN'S CANDIDATE. Cycle 1's candidate A is
// validated and committed on the ref. Every cycle-2 invocation then rewrites
// main.go and returns a valid f1 with an ordinary error, so the landed handoff
// counts three incomplete attempts and ends IMPLEMENTER_INCOMPLETE without the
// worktree ever passing the post-turn capture. What the last errored turn left
// is the current candidate: the terminal states it PRESENT, judged against its
// own captured tree, and nothing of candidate A -- not its commit, tree or
// digest -- is stated as its identity.
//
// Fails if the errored path returns before observing the worktree: the
// observation stays certified candidate A, and the ref, which holds A, is
// stated as the identity of a candidate that no longer exists.
func TestDF41A4AnErroredTurnsCandidateIsRecordedBeforeItsHandoff(t *testing.T) {
	partial := accounting(answerCode("f1"))
	crash := func(provider string, value int) incompleteTurn {
		return incompleteTurn{provider: provider, value: value, report: partial, inv: plainReturn(partial),
			err: errors.New("the worker crashed: exit status 2")}
	}
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		crash("", 2), crash("gemini", 3), crash("codex", 4),
		incompleteTurn{provider: "fourth", value: 5, report: accounting(answerCode("f2"))},
	)
	r.configure("gemini", "codex", "fourth")
	r.h.engine.Config.Validation = committingValidation()
	failed, _ := runImplement(r.h)
	var typed *ImplementerIncomplete
	if !errors.As(failed, &typed) || typed.Attempts != 3 || !equalInts(r.cycles(), 1, 2, 2, 2) || r.next != 4 {
		t.Fatalf("premise: three errored handoffs did not end IMPLEMENTER_INCOMPLETE at attempt 3: %v (cycles %v, turns %d)",
			failed, r.cycles(), r.next)
	}
	aCommit, aTree, _, aDigest := candidateRefIdentity(t, r.h)
	now := currentCapture(t, r)
	if !strings.Contains(now.Diff, "println(4)") || now.Tree == aTree {
		t.Fatalf("premise: the worktree is not the last errored turn's candidate: %s", now.Diff)
	}
	x := terminate(t, r, failed)
	typedOutcomeOf(t, r, x)
	rc := x.receipt
	if rc.CandidateState != runreceipt.CandidatePresent {
		t.Fatalf("candidate_state %s; the errored turn left a candidate", rc.CandidateState)
	}
	mentions(t, rc, "candidate A", aCommit, aTree, aDigest)
	if !strings.Contains(rc.CandidateCommit.Detail, now.Tree) {
		t.Fatalf("candidate_commit = %+v; want it judged against the current captured tree %s", rc.CandidateCommit, now.Tree)
	}
	noAbsence(t, rc)
}

// DF-41A4 cycle 3 f1 AN EMPTY POST-FORMAT CAPTURE, THEN A FAILED RECAPTURE.
// On the third cycle-2 attempt validation's formatter restores main.go to the
// base -- validate's recapture succeeds and is canonically empty -- and the
// format verification after it corrupts the index, so the post-validation
// recapture fails. The last successful observation is no current candidate,
// so the failure is UNKNOWN, never PRESENT: the candidate measured before the
// formatter is not the one the failure is about.
//
// Fails if validate does not record its recapture: the failure is judged
// against the pre-format PRESENT candidate and stated PRESENT.
func TestDF41A4AnEmptyPostFormatCaptureThenAFailedRecaptureIsUnknown(t *testing.T) {
	r := exhaustingRig(t, 2)
	v := formattingValidation()
	v.Format = append(v.Format, whenPrinting(4, `git checkout -q -- main.go && touch "$(git rev-parse --git-path df41a4-emptied)"`))
	v.FormatVerify = append(v.FormatVerify, config.Command{Command: "sh", Args: []string{"-c",
		`if [ -f "$(git rev-parse --git-path df41a4-emptied)" ]; then printf garbage > "$(git rev-parse --git-path index)"; fi; true`}})
	r.h.engine.Config.Validation = v
	_, err := r.run()
	var typed *ImplementerIncomplete
	if err == nil || errors.As(err, &typed) || !strings.Contains(err.Error(), "re-measure the candidate after validation") {
		t.Fatalf("premise: the recapture after validation did not fail: %v", err)
	}
	if got := r.cycles(); !equalInts(got, 1, 2, 2, 2) {
		t.Fatalf("premise: attempt 3 of cycle 2 was not reached: %v", got)
	}
	for _, end := range []error{err, &ImplementerIncomplete{State: ImplementerIncompleteState, TaskID: "task-1",
		PlanAttemptID: r.h.engine.operativePlanAttempt("task-1").ID, Cycle: 2, Attempts: 3, MaxAttempts: 3, Owed: []string{"f2"}}} {
		rc := terminate(t, r, end).receipt
		if rc.CandidateState != runreceipt.CandidateUnknown {
			t.Fatalf("candidate_state %s; the last successful capture held no candidate and the next one failed", rc.CandidateState)
		}
		for name, v := range candidateFields(rc) {
			if v.State != runreceipt.Unknown || !strings.Contains(v.Detail, postValidationCapture+" failed") {
				t.Errorf("%s = %+v, want UNKNOWN with the recapture's exact failure", name, v)
			}
		}
	}
}

// DF-41A4 cycle 3 f2 A FAILED RENDERING OF A REF THE TREE PROVES. The
// certified candidate is committed on its ref, so the ref's tree is the
// captured tree, and only base..ref's rendering fails. Tree equality alone
// establishes that the ref holds the candidate: commit, tree and first parent
// stay measured from the ref, the digest stays the certified capture's, and
// the rendering failure is reported only on the rendering field. Through the
// production terminal, then in identityFromRef with its refusing control.
func TestDF41A4AFailedRenderingOfARefTheTreeProvesKeepsTheRest(t *testing.T) {
	r := exhaustingRig(t, 2)
	r.h.engine.Config.Validation = committingValidation()
	restore := readCandidateRef
	injected := errors.New("diff unrenderable (injected by the DF-41A4 f2 witness)")
	readCandidateRef = func(ctx context.Context, repo gitx.Repo, branch, base string) candidateRef {
		ref := restore(ctx, repo, branch, base)
		ref.rendering, ref.renderErr = "", injected
		return ref
	}
	defer func() { readCandidateRef = restore }()

	x := endIncomplete(t, r)
	typedOutcomeOf(t, r, x)
	rc := x.receipt
	commit, tree, parent, digest := candidateRefIdentity(t, r.h)
	if rc.CandidateState != runreceipt.CandidatePresent {
		t.Fatalf("candidate_state %s", rc.CandidateState)
	}
	for name, c := range map[string]struct {
		v    runreceipt.Value
		want string
	}{"candidate_commit": {rc.CandidateCommit, commit}, "candidate_tree": {rc.CandidateTree, tree}, "candidate_first_parent": {rc.CandidateFirstParent, parent}} {
		if c.v.State != runreceipt.Known || c.v.Text != c.want {
			t.Errorf("a failed rendering erased %s: %+v, want %s", name, c.v, c.want)
		}
	}
	if rc.CandidateDigest.Text != digest || rc.CandidateDigest.Source != candidateDigestSource {
		t.Fatalf("candidate_digest = %+v; want the certified digest %s", rc.CandidateDigest, digest)
	}
	if !strings.Contains(rc.CandidateCommitDiffDigest.Detail, injected.Error()) || !strings.Contains(rc.CandidateCommitDiffDigest.Detail, commit) {
		t.Fatalf("candidate_commit_diff_digest does not carry the exact rendering failure: %+v", rc.CandidateCommitDiffDigest)
	}
	noAbsence(t, rc)

	validated := runreceipt.MeasuredValue("digest-B", candidateDigestSource)
	captured := runreceipt.MeasuredValue("tree-B", "the canonical tree the capture froze")
	c, tr, p, d := identityFromRef(candidateRef{ref: "refs/heads/c", commit: "commit-B", tree: "tree-B", parent: "base",
		renderErr: injected}, "base", validated, captured)
	if c.Text != "commit-B" || tr.Text != "tree-B" || p.Text != "base" || d != validated {
		t.Fatalf("tree equality did not keep the ref's measurements: %+v %+v %+v %+v", c, tr, p, d)
	}
	c, tr, p, d = identityFromRef(candidateRef{ref: "refs/heads/c", commit: "commit-A", tree: "tree-A", parent: "base",
		renderErr: injected}, "base", validated, captured)
	for name, v := range map[string]runreceipt.Value{"commit": c, "tree": tr, "parent": p} {
		if v.State != runreceipt.Unknown {
			t.Errorf("control: a ref neither tree nor rendering proves was merged: %s = %+v", name, v)
		}
	}
	if d != validated {
		t.Fatalf("control: the certified digest did not stand: %+v", d)
	}
}

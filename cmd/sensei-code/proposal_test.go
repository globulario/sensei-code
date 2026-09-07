package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/control"
	"github.com/globulario/sensei-code/internal/ghwebhook"
)

// Approving a GitHub objective proposal spends an at-most-once token. These
// pin the order in which it may be spent.
//
// The defect these exist for: BeginApproval used to run before the objective
// was sent, and the authority decision happens inside the server AFTER the
// connection is made. So a same-UID caller with no controlling terminal, or a
// governed descendant, could run `proposal approve`, write the durable
// receipt, and only then be refused. No task was created and the proposal was
// permanently unapprovable — an authority refusal consumed the very thing
// authority was protecting, and any local process able to run the CLI could
// destroy a pending proposal while holding no authority at all.

const testProposalComment = 3001

// approvalsDir is where the at-most-once tokens live.
func approvalsDir(root string) string {
	return filepath.Join(root, ".sensei-code", "github-objective-proposals", "approvals")
}

func countApprovals(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(approvalsDir(root))
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatalf("read approvals: %v", err)
	}
	return len(entries)
}

// fakeConn is an already-authorized objective connection.
type fakeConn struct {
	submits   int
	got       string
	accepted  control.LocalAccepted
	submitErr error
	closes    int
}

func (c *fakeConn) Submit(task string) (control.LocalAccepted, error) {
	c.submits++
	c.got = task
	if c.submitErr != nil {
		return control.LocalAccepted{}, c.submitErr
	}
	return c.accepted, nil
}

func (c *fakeConn) Close() error { c.closes++; return nil }

// recordingAuthorizer stands where control.DialLocalObjective stands, and
// records the state of the approvals directory AT THE MOMENT authority was
// decided. That observation is the ordering proof: if a receipt already exists
// when authority is asked for, the order is wrong.
type recordingAuthorizer struct {
	t                    *testing.T
	root                 string
	calls                int
	approvalsAtAuthorize int
	refuse               error
	conn                 *fakeConn
}

func (a *recordingAuthorizer) authorize(string) (authorizedSubmitter, error) {
	a.calls++
	a.approvalsAtAuthorize = countApprovals(a.t, a.root)
	if a.refuse != nil {
		return nil, a.refuse
	}
	return a.conn, nil
}

const wantObjective = "Repair the objective bridge without widening authority.\nKeep this second line exact."

func seedProposal(t *testing.T) (root string, store *ghwebhook.ProposalStore) {
	t.Helper()
	root = t.TempDir()
	return root, seedProposalIn(t, root)
}

func seedProposalIn(t *testing.T, root string) *ghwebhook.ProposalStore {
	t.Helper()
	store := ghwebhook.NewProposalStore(root)
	d := ghwebhook.IssueCommentDelivery{
		DeliveryID:         "delivery-approval",
		Action:             "created",
		RepositoryID:       1335129805,
		RepositoryFullName: "globulario/sensei-code",
		IssueNumber:        156,
		CommentID:          testProposalComment,
		CommentBody: ghwebhook.ObjectiveProposalMarker +
			"\n{\"objective\":\"Repair the objective bridge without widening authority.\\nKeep this second line exact.\"}",
		SenderID:    1697116,
		SenderLogin: "davecourtois",
	}
	if _, created, handled, err := store.Record(d); err != nil || !created || !handled {
		t.Fatalf("record: created=%v handled=%v err=%v", created, handled, err)
	}
	return store
}

func okAuthorizer(t *testing.T, root string) *recordingAuthorizer {
	return &recordingAuthorizer{t: t, root: root, conn: &fakeConn{
		accepted: control.LocalAccepted{
			TaskID: "task-3001", Provenance: "submitted-by-local-operator", Workspace: "sensei-code",
		},
	}}
}

// ---------------------------------------------------------------- the law

// THE LAW: an unauthorized same-UID process cannot consume a pending GitHub
// objective proposal.
//
// Refusal must leave the proposal exactly as it was found — still pending,
// still carrying its digest, and still approvable by someone who does hold
// authority. The last clause is the one that matters: proving the receipt is
// absent is weaker than proving the proposal still WORKS afterwards.
func TestAnUnauthorizedSameUIDProcessCannotConsumeAPendingProposal(t *testing.T) {
	// The two refusals the server actually produces for a same-UID caller.
	refusals := map[string]error{
		"no controlling terminal": errors.New("pid 5150 has no controlling terminal; an objective is placed by an operator at one, " +
			"and running as this user is not by itself authority to originate governed work"),
		"governed descendant": errors.New("pid 5151 is a process this orchestrator launched, and a worker may not originate " +
			"governed work: implementing one objective does not confer the authority to create another"),
	}

	for name, refusal := range refusals {
		t.Run(name, func(t *testing.T) {
			root, store := seedProposal(t)
			before, err := store.Load(testProposalComment)
			if err != nil {
				t.Fatalf("load: %v", err)
			}

			auth := okAuthorizer(t, root)
			auth.refuse = refusal

			var out bytes.Buffer
			err = approveObjectiveProposal(root, testProposalComment, &out, auth.authorize, nil)
			if err == nil {
				t.Fatal("an unauthorized caller approved the proposal")
			}
			if !strings.Contains(err.Error(), "remains pending") {
				t.Errorf("the refusal does not say the proposal is untouched: %v", err)
			}

			// Nothing durable was spent.
			if n := countApprovals(t, root); n != 0 {
				t.Fatalf("an unauthorized caller left %d approval receipts; the proposal is spent", n)
			}
			if _, ok, err := store.Approval(testProposalComment); err != nil || ok {
				t.Fatalf("an approval receipt exists after refusal: ok=%v err=%v", ok, err)
			}
			if auth.conn.submits != 0 {
				t.Error("a refused caller still submitted an objective")
			}

			// The proposal survives byte-for-byte.
			after, err := store.Load(testProposalComment)
			if err != nil {
				t.Fatalf("the proposal was damaged by a refusal: %v", err)
			}
			if after.Objective != before.Objective || after.ObjectiveDigest != before.ObjectiveDigest {
				t.Fatalf("the proposal changed:\n before %q %s\n after  %q %s",
					before.Objective, before.ObjectiveDigest, after.Objective, after.ObjectiveDigest)
			}

			// And it is still approvable by someone who does hold authority.
			holder := okAuthorizer(t, root)
			if err := approveObjectiveProposal(root, testProposalComment, &bytes.Buffer{}, holder.authorize, nil); err != nil {
				t.Fatalf("the proposal was permanently spent by a refusal: %v", err)
			}
			if holder.conn.got != wantObjective {
				t.Errorf("the surviving proposal submitted %q", holder.conn.got)
			}
		})
	}
}

// ------------------------------------------------------------- the ordering

// Authority is established BEFORE any durable receipt exists. Observed rather
// than asserted from source order: the authorizer reads the approvals
// directory at the moment it is called.
func TestAuthorityIsEstablishedBeforeAnyDurableReceipt(t *testing.T) {
	root, _ := seedProposal(t)
	auth := okAuthorizer(t, root)

	if err := approveObjectiveProposal(root, testProposalComment, &bytes.Buffer{}, auth.authorize, nil); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if auth.calls != 1 {
		t.Fatalf("authority was established %d times, want exactly 1", auth.calls)
	}
	if auth.approvalsAtAuthorize != 0 {
		t.Fatalf("%d approval receipts already existed when authority was asked for; "+
			"the token is being spent before the caller is known to hold any", auth.approvalsAtAuthorize)
	}
	if n := countApprovals(t, root); n != 1 {
		t.Fatalf("%d approval receipts after a successful approval, want 1", n)
	}
}

// A failure between authorization and BeginApproval leaves no receipt either.
func TestAFailureBeforeBeginApprovalLeavesZeroReceipt(t *testing.T) {
	root, _ := seedProposal(t)

	// The proposal does not exist: approve refuses before it ever authorizes,
	// and certainly before anything durable.
	auth := okAuthorizer(t, root)
	if err := approveObjectiveProposal(root, 999999, &bytes.Buffer{}, auth.authorize, nil); err == nil {
		t.Fatal("approving a proposal that does not exist succeeded")
	}
	if n := countApprovals(t, root); n != 0 {
		t.Fatalf("%d receipts written for a proposal that does not exist", n)
	}

	// And an authorizer that fails for a transport reason -- no owner running --
	// is likewise not charged against the proposal.
	down := okAuthorizer(t, root)
	down.refuse = errors.New("no control process is accepting objectives; start one with `sensei-code control`")
	if err := approveObjectiveProposal(root, testProposalComment, &bytes.Buffer{}, down.authorize, nil); err == nil {
		t.Fatal("approval succeeded with no control process")
	}
	if n := countApprovals(t, root); n != 0 {
		t.Fatalf("%d receipts written when no control process was running", n)
	}
}

// ------------------------------------------------------- fail-closed, intact

// After authority succeeded and the receipt was written, a lost submission
// stays ambiguous. This is UNCHANGED by the repair and must remain so: once
// the objective is on the wire, whether a task was created is not safely
// inferable, and a retry could launch a second Claude on the same objective.
func TestALostSubmissionAfterBeginApprovalStaysAttemptingAndCannotRetry(t *testing.T) {
	root, store := seedProposal(t)

	auth := okAuthorizer(t, root)
	auth.conn.submitErr = errors.New("the connection died after the objective was written")

	err := approveObjectiveProposal(root, testProposalComment, &bytes.Buffer{}, auth.authorize, nil)
	if err == nil {
		t.Fatal("a lost submission reported success")
	}
	if !strings.Contains(err.Error(), "do not retry automatically") {
		t.Errorf("the failure does not warn against automatic retry: %v", err)
	}
	if auth.conn.submits != 1 {
		t.Fatalf("submitted %d times", auth.conn.submits)
	}

	// The receipt is durable and stuck at attempting.
	a, ok, err := store.Approval(testProposalComment)
	if err != nil || !ok {
		t.Fatalf("no durable receipt after a lost submission: ok=%v err=%v", ok, err)
	}
	if a.State != "attempting" {
		t.Fatalf("approval state = %q, want attempting", a.State)
	}
	if a.TaskID != "" {
		t.Errorf("a lost submission recorded task %q", a.TaskID)
	}

	// And it cannot be retried into a second submission.
	retry := okAuthorizer(t, root)
	if err := approveObjectiveProposal(root, testProposalComment, &bytes.Buffer{}, retry.authorize, nil); err == nil {
		t.Fatal("an ambiguous approval was retried")
	}
	if retry.conn.submits != 0 {
		t.Fatalf("the retry reached the objective channel %d times", retry.conn.submits)
	}
}

// ------------------------------------------------------------ the happy path

// The exact stored bytes, and the digest that names them, are bound to the
// task the local channel accepted.
func TestSuccessBindsTheExactStoredObjectiveDigestToTheAcceptedTask(t *testing.T) {
	root, store := seedProposal(t)
	stored, err := store.Load(testProposalComment)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	auth := okAuthorizer(t, root)
	var out bytes.Buffer
	if err := approveObjectiveProposal(root, testProposalComment, &out, auth.authorize, nil); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Exact bytes, not a re-fetch and not argv.
	if auth.conn.got != wantObjective {
		t.Fatalf("submitted %q, want the exact stored objective %q", auth.conn.got, wantObjective)
	}
	if auth.conn.submits != 1 {
		t.Fatalf("submitted %d times", auth.conn.submits)
	}
	if auth.conn.closes == 0 {
		t.Error("the authorized connection was not closed")
	}

	a, ok, err := store.Approval(testProposalComment)
	if err != nil || !ok {
		t.Fatalf("no receipt: ok=%v err=%v", ok, err)
	}
	if a.State != "submitted" || a.TaskID != "task-3001" || a.Nonce == "" {
		t.Fatalf("receipt = %+v", a)
	}
	if a.ObjectiveDigest != stored.ObjectiveDigest {
		t.Fatalf("receipt digest %s does not name the stored objective %s", a.ObjectiveDigest, stored.ObjectiveDigest)
	}
	if a.ObjectiveDigest != ghwebhook.DigestObjective(wantObjective) {
		t.Fatalf("digest %s is not SHA-256 of the exact objective bytes", a.ObjectiveDigest)
	}
	if !strings.Contains(out.String(), a.ObjectiveDigest) || !strings.Contains(out.String(), "task-3001") {
		t.Fatalf("receipt output omitted the binding: %s", out.String())
	}

	// Approved once, and only once.
	second := okAuthorizer(t, root)
	if err := approveObjectiveProposal(root, testProposalComment, &out, second.authorize, nil); err == nil {
		t.Fatal("a second approval was accepted")
	}
	if second.conn.submits != 0 {
		t.Fatalf("the second approval reached the objective channel %d times", second.conn.submits)
	}
}

// -------------------------------------------------- exact objective bytes

// A proposal whose objective carries surrounding whitespace must submit those
// bytes unchanged, and its digest must name them.
//
// The full invariant this defends:
//
//	bytes accepted from the stored proposal
//	  == bytes sent over the authorized connection
//	  == bytes recorded as the workflow objective
//	  == bytes hashed into the architecture binding
//
// This test owns the first two links. internal/control owns the wire, and
// internal/workflow owns the record and the digest.
func TestAProposalWithWhitespaceSubmitsItsExactBytes(t *testing.T) {
	const exact = "  exact objective bytes  \n"

	root := t.TempDir()
	store := ghwebhook.NewProposalStore(root)
	body, err := json.Marshal(map[string]string{"objective": exact})
	if err != nil {
		t.Fatal(err)
	}
	d := ghwebhook.IssueCommentDelivery{
		DeliveryID:         "delivery-whitespace",
		Action:             "created",
		RepositoryID:       1335129805,
		RepositoryFullName: "globulario/sensei-code",
		IssueNumber:        156,
		CommentID:          4242,
		CommentBody:        ghwebhook.ObjectiveProposalMarker + "\n" + string(body),
		SenderID:           1697116,
		SenderLogin:        "davecourtois",
	}
	if _, created, handled, err := store.Record(d); err != nil || !created || !handled {
		t.Fatalf("record: created=%v handled=%v err=%v", created, handled, err)
	}

	// Stored exactly, and the digest names those bytes.
	stored, err := store.Load(4242)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if stored.Objective != exact {
		t.Fatalf("the proposal store altered the objective:\n got  %q\n want %q", stored.Objective, exact)
	}
	if stored.ObjectiveDigest != ghwebhook.DigestObjective(exact) {
		t.Fatalf("stored digest %s does not name the exact bytes", stored.ObjectiveDigest)
	}

	// Approved, those exact bytes cross the authorized connection.
	auth := okAuthorizer(t, root)
	auth.conn.accepted = control.LocalAccepted{
		TaskID: "task-4242", Provenance: "submitted-by-local-operator", Workspace: "sensei-code",
	}
	if err := approveObjectiveProposal(root, 4242, &bytes.Buffer{}, auth.authorize, nil); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if auth.conn.got != exact {
		t.Fatalf("approval submitted altered bytes:\n got  %q\n want %q", auth.conn.got, exact)
	}

	// And the receipt binds the digest of those same bytes to the task.
	a, ok, err := store.Approval(4242)
	if err != nil || !ok {
		t.Fatalf("no receipt: ok=%v err=%v", ok, err)
	}
	if a.ObjectiveDigest != ghwebhook.DigestObjective(exact) {
		t.Fatalf("receipt digest %s does not name the exact submitted bytes", a.ObjectiveDigest)
	}
	if a.TaskID != "task-4242" {
		t.Fatalf("receipt task = %q", a.TaskID)
	}
}

// ------------------------------------------------- the cleanliness precheck

// A precondition the governed run will apply anyway must not cost a token to
// discover.
//
// The first real run of this path spent proposal 5561620357 exactly that way:
// authority passed, BeginApproval wrote the receipt, the task was created with
// the correct objective and provenance, and candidate.Establish refused one
// second later because two untracked runtime files made the checkout dirty.
// That condition was true before the caller connected and readable in one
// `git status`.

// gitRepo builds a real repository so the precheck is proved against git's own
// notion of clean, not a restatement of it.
func gitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v\n%s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "seed.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The real repository ignores .sensei-code/, so the proposal store itself
	// does not dirty the tree. Without this the fixture would be dirty for a
	// reason the product never has, and the test would prove the wrong thing.
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".sensei-code/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "seed"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return root
}

// The exact shape that spent the first proposal: UNTRACKED files only.
func TestADirtyCheckoutIsRefusedBeforeTheTokenIsSpent(t *testing.T) {
	root := gitRepo(t)
	seedProposalIn(t, root)

	// Clean at this point, so the run would proceed.
	if err := canonicalIsClean(root); err != nil {
		t.Fatalf("a freshly committed repository is not clean: %v", err)
	}

	// Exactly what dirtied it in the real run: untracked, not modified.
	if err := os.WriteFile(filepath.Join(root, "briefing-delivery.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := canonicalIsClean(root); err == nil {
		t.Fatal("an untracked file did not make the checkout dirty; the precheck disagrees with candidate.Establish")
	}

	auth := okAuthorizer(t, root)
	err := approveObjectiveProposal(root, testProposalComment, &bytes.Buffer{}, auth.authorize, canonicalIsClean)
	if err == nil {
		t.Fatal("a dirty checkout was approved")
	}
	if !strings.Contains(err.Error(), "remains pending") {
		t.Errorf("the refusal does not say the proposal is untouched: %v", err)
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("the refusal does not carry candidate.Establish's own message: %v", err)
	}

	// Nothing durable was spent, and nothing was submitted.
	if n := countApprovals(t, root); n != 0 {
		t.Fatalf("a dirty checkout left %d approval receipts", n)
	}
	if auth.conn.submits != 0 {
		t.Error("a dirty checkout still submitted an objective")
	}

	// And the proposal survives: cleaning the tree makes it approvable again.
	if err := os.Remove(filepath.Join(root, "briefing-delivery.jsonl")); err != nil {
		t.Fatal(err)
	}
	after := okAuthorizer(t, root)
	if err := approveObjectiveProposal(root, testProposalComment, &bytes.Buffer{}, after.authorize, canonicalIsClean); err != nil {
		t.Fatalf("the proposal was spent by a dirty-tree refusal: %v", err)
	}
	if after.conn.got != wantObjective {
		t.Errorf("submitted %q", after.conn.got)
	}
}

// Authority is still decided FIRST. An unauthorized caller learns that it may
// not originate work, not the state of the operator's working tree.
func TestAuthorityIsDecidedBeforeCleanliness(t *testing.T) {
	root := gitRepo(t)
	seedProposalIn(t, root)
	if err := os.WriteFile(filepath.Join(root, "untracked"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	auth := okAuthorizer(t, root)
	auth.refuse = errors.New("pid 5150 has no controlling terminal")

	err := approveObjectiveProposal(root, testProposalComment, &bytes.Buffer{}, auth.authorize, canonicalIsClean)
	if err == nil {
		t.Fatal("an unauthorized caller was accepted")
	}
	if strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("an unauthorized caller was told about the working tree: %v", err)
	}
	if !strings.Contains(err.Error(), "controlling terminal") {
		t.Errorf("the refusal is not the authority one: %v", err)
	}
	if n := countApprovals(t, root); n != 0 {
		t.Fatalf("%d receipts written", n)
	}
}

// A precheck failure that is not about cleanliness still refuses before the
// token is spent, and says what it could not establish.
func TestAnUnreadableCheckoutRefusesBeforeTheTokenIsSpent(t *testing.T) {
	root := t.TempDir() // not a git repository at all
	seedProposalIn(t, root)

	auth := okAuthorizer(t, root)
	err := approveObjectiveProposal(root, testProposalComment, &bytes.Buffer{}, auth.authorize, canonicalIsClean)
	if err == nil {
		t.Fatal("a repository whose state could not be read was approved")
	}
	if n := countApprovals(t, root); n != 0 {
		t.Fatalf("%d receipts written for an unreadable checkout", n)
	}
	if auth.conn.submits != 0 {
		t.Error("an unreadable checkout still submitted an objective")
	}
}

// The precheck does not REPLACE candidate.Establish's check. The tree can be
// dirtied after approval begins, and that later refusal legitimately spends the
// proposal -- by then a task exists.
func TestTheLaterEstablishCheckIsRetained(t *testing.T) {
	src, err := os.ReadFile("../../internal/candidate/identity.go")
	if err != nil {
		t.Fatalf("read identity.go: %v", err)
	}
	text := string(src)
	if !strings.Contains(text, "clean, err := repo.IsClean()") {
		t.Fatal("candidate.Establish no longer checks cleanliness; the precheck is not a replacement for it")
	}
	if !strings.Contains(text, "&ErrDirtyCanonical{Repository: repoRoot}") {
		t.Fatal("candidate.Establish no longer refuses a dirty canonical checkout")
	}

	// And the precheck is genuinely the same predicate, not a lookalike.
	approve, err := os.ReadFile("proposal.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"gitx.Repo{Root: repoRoot}.IsClean(",
		"&candidate.ErrDirtyCanonical{Repository: repoRoot}",
	} {
		if !strings.Contains(string(approve), want) {
			t.Errorf("the precheck does not reuse %q; it would drift from the gate it is predicting", want)
		}
	}
}

package main

import (
	"bytes"
	"errors"
	"os"
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
	store = ghwebhook.NewProposalStore(root)
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
	return root, store
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
			err = approveObjectiveProposal(root, testProposalComment, &out, auth.authorize)
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
			if err := approveObjectiveProposal(root, testProposalComment, &bytes.Buffer{}, holder.authorize); err != nil {
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

	if err := approveObjectiveProposal(root, testProposalComment, &bytes.Buffer{}, auth.authorize); err != nil {
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
	if err := approveObjectiveProposal(root, 999999, &bytes.Buffer{}, auth.authorize); err == nil {
		t.Fatal("approving a proposal that does not exist succeeded")
	}
	if n := countApprovals(t, root); n != 0 {
		t.Fatalf("%d receipts written for a proposal that does not exist", n)
	}

	// And an authorizer that fails for a transport reason -- no owner running --
	// is likewise not charged against the proposal.
	down := okAuthorizer(t, root)
	down.refuse = errors.New("no control process is accepting objectives; start one with `sensei-code control`")
	if err := approveObjectiveProposal(root, testProposalComment, &bytes.Buffer{}, down.authorize); err == nil {
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

	err := approveObjectiveProposal(root, testProposalComment, &bytes.Buffer{}, auth.authorize)
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
	if err := approveObjectiveProposal(root, testProposalComment, &bytes.Buffer{}, retry.authorize); err == nil {
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
	if err := approveObjectiveProposal(root, testProposalComment, &out, auth.authorize); err != nil {
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
	if err := approveObjectiveProposal(root, testProposalComment, &out, second.authorize); err == nil {
		t.Fatal("a second approval was accepted")
	}
	if second.conn.submits != 0 {
		t.Fatalf("the second approval reached the objective channel %d times", second.conn.submits)
	}
}

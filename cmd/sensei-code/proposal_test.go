package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/control"
	"github.com/globulario/sensei-code/internal/ghwebhook"
)

func TestProposalApprovalSubmitsTheExactStoredObjectiveOnce(t *testing.T) {
	root := t.TempDir()
	store := ghwebhook.NewProposalStore(root)
	want := "Repair the objective bridge without widening authority.\nKeep this second line exact."
	d := ghwebhook.IssueCommentDelivery{
		DeliveryID:         "delivery-approval",
		Action:             "created",
		RepositoryID:       1335129805,
		RepositoryFullName: "globulario/sensei-code",
		IssueNumber:        156,
		CommentID:          3001,
		CommentBody:        ghwebhook.ObjectiveProposalMarker + "\n{\"objective\":\"Repair the objective bridge without widening authority.\\nKeep this second line exact.\"}",
		SenderID:           1697116,
		SenderLogin:        "davecourtois",
	}
	if _, created, handled, err := store.Record(d); err != nil || !created || !handled {
		t.Fatalf("record: created=%v handled=%v err=%v", created, handled, err)
	}

	calls := 0
	var got string
	fake := func(repoRoot, task string) (control.LocalAccepted, error) {
		calls++
		got = task
		return control.LocalAccepted{TaskID: "task-3001", Provenance: "submitted-by-local-operator", Workspace: "sensei-code"}, nil
	}
	var out bytes.Buffer
	if err := approveObjectiveProposal(root, 3001, &out, fake); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || got != want {
		t.Fatalf("submission calls=%d objective=%q want=%q", calls, got, want)
	}
	a, ok, err := store.Approval(3001)
	if err != nil || !ok || a.State != "submitted" || a.TaskID != "task-3001" || a.Nonce == "" {
		t.Fatalf("approval receipt: ok=%v receipt=%+v err=%v", ok, a, err)
	}
	if !strings.Contains(out.String(), a.ObjectiveDigest) || !strings.Contains(out.String(), "task-3001") {
		t.Fatalf("receipt output omitted binding: %s", out.String())
	}

	if err := approveObjectiveProposal(root, 3001, &out, fake); err == nil {
		t.Fatal("second approval was accepted")
	}
	if calls != 1 {
		t.Fatalf("second approval reached submitter; calls=%d", calls)
	}
}

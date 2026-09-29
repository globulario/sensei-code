package ghwebhook

import (
	"errors"
	"testing"
	"time"
)

func proposalDelivery(delivery string, comment int64, objective string) IssueCommentDelivery {
	return IssueCommentDelivery{
		DeliveryID:         delivery,
		Action:             "created",
		InstallationID:     159521273,
		RepositoryID:       1335129805,
		RepositoryFullName: "globulario/sensei-code",
		IssueNumber:        156,
		CommentID:          comment,
		CommentBody:        ObjectiveProposalMarker + "\n{\"objective\":" + objective + "}",
		SenderID:           1697116,
		SenderLogin:        "davecourtois",
		SenderType:         "User",
	}
}

func TestObjectiveProposalProtocolIsStrictAndPreservesTheString(t *testing.T) {
	if _, handled, err := ParseObjectiveProposal("ordinary comment"); handled || err != nil {
		t.Fatalf("ordinary comment: handled=%v err=%v", handled, err)
	}
	want := "Fix exactly this.\nKeep the second line."
	body := ObjectiveProposalMarker + "\n{\"objective\":\"Fix exactly this.\\nKeep the second line.\"}"
	got, handled, err := ParseObjectiveProposal(body)
	if err != nil || !handled {
		t.Fatalf("valid proposal: handled=%v err=%v", handled, err)
	}
	if got != want {
		t.Fatalf("objective changed: got %q want %q", got, want)
	}
	for _, body := range []string{
		ObjectiveProposalMarker,
		ObjectiveProposalMarker + "\n{}",
		ObjectiveProposalMarker + "\n{\"objective\":\"x\",\"authority\":true}",
		ObjectiveProposalMarker + "\n{\"objective\":\"x\"} trailing",
	} {
		if _, handled, err := ParseObjectiveProposal(body); !handled || !errors.Is(err, ErrMalformedObjectiveProposal) {
			t.Fatalf("malformed proposal %q: handled=%v err=%v", body, handled, err)
		}
	}
}

func TestProposalStoreDeduplicatesDeliveryAndCommentDurably(t *testing.T) {
	store := NewProposalStore(t.TempDir())
	first := proposalDelivery("delivery-a", 1001, "\"fix the bridge\"")
	p, created, handled, err := store.Record(first)
	if err != nil || !created || !handled {
		t.Fatalf("first record: created=%v handled=%v err=%v", created, handled, err)
	}
	if p.Objective != "fix the bridge" || p.ObjectiveDigest == "" {
		t.Fatalf("bad proposal: %+v", p)
	}

	redelivery := first
	redelivery.DeliveryID = "delivery-b"
	if _, created, handled, err := store.Record(redelivery); err != nil || created || !handled {
		t.Fatalf("redelivery: created=%v handled=%v err=%v", created, handled, err)
	}
	list, err := store.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list after redelivery: len=%d err=%v", len(list), err)
	}

	changed := first
	changed.DeliveryID = "delivery-c"
	changed.CommentBody = ObjectiveProposalMarker + "\n{\"objective\":\"different bytes\"}"
	if _, _, _, err := store.Record(changed); err == nil {
		t.Fatal("same comment id with different objective bytes was accepted")
	}

	reusedDelivery := proposalDelivery("delivery-a", 1002, "\"another objective\"")
	if _, _, _, err := store.Record(reusedDelivery); err == nil {
		t.Fatal("same delivery id was rebound to another comment")
	}
}

func TestApprovalAttemptIsDurableAndAtMostOnce(t *testing.T) {
	store := NewProposalStore(t.TempDir())
	p, _, _, err := store.Record(proposalDelivery("delivery-a", 2001, "\"repair x\""))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 6, 16, 30, 0, 0, time.UTC)
	_, a, err := store.BeginApproval(p.CommentID, at, "nonce-1")
	if err != nil {
		t.Fatal(err)
	}
	if a.State != "attempting" || a.ObjectiveDigest != p.ObjectiveDigest {
		t.Fatalf("bad approval attempt: %+v", a)
	}
	if _, _, err := store.BeginApproval(p.CommentID, at.Add(time.Second), "nonce-2"); err == nil {
		t.Fatal("second approval attempt was accepted")
	}
	completed, err := store.CompleteApproval(p.CommentID, "nonce-1", "task-1", "submitted-by-local-operator", at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != "submitted" || completed.TaskID != "task-1" {
		t.Fatalf("bad completed receipt: %+v", completed)
	}
	loaded, ok, err := store.Approval(p.CommentID)
	if err != nil || !ok || loaded.Nonce != "nonce-1" || loaded.TaskID != "task-1" {
		t.Fatalf("loaded receipt: ok=%v receipt=%+v err=%v", ok, loaded, err)
	}
}

// W4 -- ONE DIGEST RULE. The proposal digest is the objective identity
// roles.BindArchitecture binds, so a proposal and the architecture turn it
// becomes cannot name different bytes. roles is outside this ingress's reach
// (boundary_test.go), so the agreement is pinned through values computed
// independently of both (sha256sum over the exact bytes): the workflow's W1 and
// W3 witnesses pin BindArchitecture's output against the SAME constant.
func TestDigestObjectiveAgreesWithTheArchitectureBinding(t *testing.T) {
	// An empty objective has no digest, exactly as BindArchitecture gives none:
	// it must not become the valid-looking SHA-256 of "".
	if got := DigestObjective(""); got != "" {
		t.Fatalf("an empty objective was digested to %q; BindArchitecture yields no digest for it", got)
	}
	const (
		exact = "\t  resume reads the recorded objective  \n"
		// sha256 of exact, byte for byte.
		exactDigest = "d60db1d22ca040881a6fec3d731ae42a0ecdad7c0c941a6885ac188ff47ea6f2"
		// sha256 of the same text trimmed: a different input, a different identity.
		trimmedDigest = "04ceb57735a2a4f974f1a701d2bde4cff09881b721193d36a4ea6d651b80b109"
	)
	if got := DigestObjective(exact); got != exactDigest {
		t.Fatalf("the digest does not name the exact untrimmed bytes: %s", got)
	}
	if got := DigestObjective("resume reads the recorded objective"); got != trimmedDigest || got == exactDigest {
		t.Fatalf("trimmed and untrimmed objectives were not kept apart: %s", got)
	}
}

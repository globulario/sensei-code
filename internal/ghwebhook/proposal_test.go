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

// W4 -- ONE DIGEST RULE. A proposal names its objective by the same rule the
// architecture binding does, so the vectors below are the ones the workflow
// witnesses pin for roles.BindArchitecture (W16 and W2 in
// internal/workflow/authority_resume_identity_test.go): a proposal and the
// architect turn it becomes cannot disagree about which bytes they name.
//
// An empty objective has NO digest, never the perfectly valid SHA-256 of "",
// and a non-empty one is hashed as its exact bytes, whitespace included.
func TestW4DigestObjectiveAgreesWithTheArchitectureBindingRule(t *testing.T) {
	if got := DigestObjective(""); got != "" {
		t.Fatalf("an empty objective was given digest %q; the architecture binding gives it none", got)
	}
	for _, v := range []struct{ objective, digest string }{
		{"restore the recorded objective across an answered authority question",
			"5549bf4fa65d8ddc670960dc4b831d39101a1955e437e072cb6986b962b15b9d"},
		// Leading spaces and a trailing newline are part of the objective.
		{"  read the recorded objective, not the handed one\n",
			"9d2364da08f00f7f0f3ea457ea5d14cd9b049730848a90731307c8e2e113a359"},
	} {
		if got := DigestObjective(v.objective); got != v.digest {
			t.Errorf("DigestObjective(%q) = %s, want the exact-byte digest %s", v.objective, got, v.digest)
		}
	}
	// Trimming would collapse two distinct objectives into one identity.
	if DigestObjective(" x") == DigestObjective("x") || DigestObjective("x\n") == DigestObjective("x") {
		t.Fatal("whitespace-distinct objectives share a digest; the bytes were normalized")
	}
}

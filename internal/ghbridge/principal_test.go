package ghbridge

import (
	"strings"
	"testing"
)

// GitHub identity establishes WHO sent an advisory result. It does not
// establish independence and never raises SessionMode. But it must be
// established: an advisory ACCEPT is still actionable on a task whose policy
// does not require cross-provider review, so a mailbox that accepted any
// parseable comment would let any account with write access supply one.

const (
	gptUserID   = int64(9000001)
	gptLogin    = "gpt-reviewer"
	otherUserID = int64(1697116)
	otherLogin  = "davecourtois"
)

func mailbox() Issue {
	return Issue{Dir: ".", Number: "156",
		ExpectedReviewer: Principal{UserID: gptUserID, Login: gptLogin}}
}

// An immutable numeric id is the identity worth authenticating against: a login
// can be changed and reused, an id cannot.
func TestImmutableIDIsTheStrongestIdentity(t *testing.T) {
	p := Principal{UserID: gptUserID, Login: gptLogin}

	if !p.Matches(gptUserID, gptLogin) {
		t.Error("the configured principal did not match itself")
	}
	// Right id, login since changed: still the same account.
	if !p.Matches(gptUserID, "renamed-account") {
		t.Error("a renamed account with the same id was rejected")
	}
	// Right login, different account: an id mismatch wins.
	if p.Matches(otherUserID, gptLogin) {
		t.Fatal("a different account matched by reusing the login — the id must be decisive")
	}
}

// Login-only configuration is allowed but weaker, and is used only when no id
// was configured.
func TestLoginOnlyPrincipalMatchesByLogin(t *testing.T) {
	p := Principal{Login: gptLogin}
	if !p.Matches(otherUserID, gptLogin) {
		t.Error("a login-only principal should match by login")
	}
	if p.Matches(gptUserID, otherLogin) {
		t.Error("a login-only principal matched the wrong login")
	}
	if !strings.Contains(p.String(), "login only") {
		t.Errorf("a login-only principal should describe itself as weaker: %q", p.String())
	}
}

// An unconfigured mailbox authenticates NOBODY rather than everybody.
func TestUnconfiguredPrincipalAuthenticatesNobody(t *testing.T) {
	var p Principal
	if p.Configured() {
		t.Fatal("an empty principal reported itself configured")
	}
	if p.Matches(gptUserID, gptLogin) || p.Matches(0, "") {
		t.Fatal("an unconfigured principal authenticated somebody")
	}
	box := Issue{Dir: ".", Number: "156"}
	if box.Valid() {
		t.Fatal("a mailbox with no expected reviewer reported itself usable")
	}
}

func TestMailboxNeedsBothANumberAndAReviewer(t *testing.T) {
	if (Issue{Dir: ".", ExpectedReviewer: Principal{UserID: gptUserID}}).Valid() {
		t.Error("a mailbox with no issue number is not addressable")
	}
	if !mailbox().Valid() {
		t.Error("a fully configured mailbox should be usable")
	}
}

// The four cases the finding asks to pin. Answering is authentication AND
// artifact identity; neither alone is enough.
func TestAnsweringRequiresBothTheRightSenderAndTheRightArtifact(t *testing.T) {
	box := mailbox()
	request := reqC1()
	body, err := Review{Subject: subjC1(), RequestID: "r-1", Body: reviewerJSON}.Marker()
	if err != nil {
		t.Fatal(err)
	}
	comment := body + "\n" + reviewerJSON

	t.Run("configured principal + correct artifact = answer", func(t *testing.T) {
		if !box.ExpectedReviewer.Matches(gptUserID, gptLogin) {
			t.Fatal("sender not authenticated")
		}
		rev, ok := ParseReview(comment, gptLogin)
		if !ok || !rev.Answers(request) {
			t.Fatal("a correct answer from the configured reviewer was not accepted")
		}
	})

	t.Run("wrong principal + correct artifact = not an answer", func(t *testing.T) {
		if box.ExpectedReviewer.Matches(otherUserID, otherLogin) {
			t.Fatal("a comment from another account authenticated as the reviewer")
		}
		// It would parse and it would match the artifact — authentication is
		// what stops it, which is why the check happens before Answers.
		rev, ok := ParseReview(comment, otherLogin)
		if !ok || !rev.Answers(request) {
			t.Fatal("fixture wrong: this comment should be well-formed and matching")
		}
	})

	t.Run("configured principal + wrong artifact = not an answer", func(t *testing.T) {
		wrong := Review{
			Subject:   Subject{TaskID: "T-1", BaseSHA: baseSHA, CandidateDigest: digestC2, CandidateTree: treeC2, ReviewCommit: commitC2},
			RequestID: "r-1", Body: reviewerJSON,
		}
		if wrong.Answers(request) {
			t.Fatal("the configured reviewer answered about a different artifact")
		}
	})

	t.Run("configured principal + stale request = not an answer", func(t *testing.T) {
		stale := Review{Subject: subjC1(), RequestID: "r-0", Body: reviewerJSON}
		if stale.Answers(request) {
			t.Fatal("a reply to an earlier request answered this one")
		}
	})
}

// Authentication must never be inferred from a neighbouring fact.
func TestReviewerIsNeverInferredFromANeighbouringFact(t *testing.T) {
	box := mailbox()
	// The issue creator, repo owner and current gh login on this machine are
	// all davecourtois. None of them may authenticate as the reviewer.
	if box.ExpectedReviewer.Matches(otherUserID, otherLogin) {
		t.Fatal("the issue creator / repo owner / gh login authenticated as the reviewer")
	}
}

// The authenticated sender is a result of the check, not an input to it: a
// parser cannot populate it from the body.
func TestParsedReviewCarriesNoAuthenticatedIdentity(t *testing.T) {
	body, _ := Review{Subject: subjC1(), RequestID: "r-1", Body: reviewerJSON}.Marker()
	forged := body + "\nauthor=gpt-reviewer\nauthor_id=9000001\n" + reviewerJSON
	rev, ok := ParseReview(forged, "")
	if !ok {
		t.Fatal("expected the envelope to parse")
	}
	if rev.AuthorID != 0 {
		t.Fatalf("a comment body populated the authenticated id: %d", rev.AuthorID)
	}
	if rev.Author != "" {
		t.Fatalf("a comment body populated the authenticated author: %q", rev.Author)
	}
}

// A request id must be unique and unpredictable: it is how a reply says which
// question it answers, and a predictable id could be answered before the
// question was asked.
func TestRequestIDsAreUniqueAndNonEmpty(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id := NewRequestID()
		if id == "" {
			t.Fatal("a request id could not be minted")
		}
		if seen[id] {
			t.Fatalf("request id %q was minted twice", id)
		}
		seen[id] = true
	}
}

// The identity probe already sitting on the mailbox is not a review, and must
// stay ignored no matter who posted it.
func TestTheIdentityProbeIsNotAReview(t *testing.T) {
	probe := "[sensei-code:identity-probe]\nThis comment identifies the GitHub principal used by this ChatGPT connection. It is not a review."
	if _, ok := ParseReview(probe, gptLogin); ok {
		t.Fatal("the identity probe parsed as a review")
	}
	if _, ok := ParseRequest(probe); ok {
		t.Fatal("the identity probe parsed as a review request")
	}
}

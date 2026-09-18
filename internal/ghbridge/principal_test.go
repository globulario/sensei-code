package ghbridge

import (
	"reflect"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/reviewartifact"
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
//
// Read through the CANONICAL grammar and the obligation, which is what the
// mailbox actually uses. The legacy pair (ParseReview + Review.Answers) that
// this used to exercise did not require reviewer=<provider> at all, so it could
// prove "the right artifact" while proving nothing about who was asked.
func TestAnsweringRequiresBothTheRightSenderAndTheRightArtifact(t *testing.T) {
	box := mailbox()
	owed := ReviewObligation{
		TaskID: "T-1", RequestID: "r-1", BaseSHA: baseSHA, CandidateDigest: digestC1,
		CandidateTree: treeC1, ReviewCommit: commitC1, ReviewerProvider: "chatgpt",
	}
	comment := canonicalAnswer(t, subjC1(), "r-1", "chatgpt", reviewerJSON)

	t.Run("configured principal + correct artifact = answer", func(t *testing.T) {
		if !box.ExpectedReviewer.Matches(gptUserID, gptLogin) {
			t.Fatal("sender not authenticated")
		}
		art, err := reviewartifact.Parse(comment)
		if err != nil {
			t.Fatalf("a correct answer did not parse: %v", err)
		}
		if m := boundMismatch(owed, art); m != "" {
			t.Fatalf("a correct answer from the configured reviewer was not accepted: %s", m)
		}
	})

	t.Run("wrong principal + correct artifact = not an answer", func(t *testing.T) {
		if box.ExpectedReviewer.Matches(otherUserID, otherLogin) {
			t.Fatal("a comment from another account authenticated as the reviewer")
		}
		// It parses and it matches the artifact -- AUTHENTICATION is what stops
		// it, which is why the principal check happens before classification.
		art, err := reviewartifact.Parse(comment)
		if err != nil {
			t.Fatalf("fixture wrong: this comment should be well-formed: %v", err)
		}
		if m := boundMismatch(owed, art); m != "" {
			t.Fatalf("fixture wrong: this comment should be matching: %s", m)
		}
	})

	t.Run("configured principal + wrong artifact = not an answer", func(t *testing.T) {
		wrong, err := reviewartifact.Parse(canonicalAnswer(t, Subject{TaskID: "T-1", BaseSHA: baseSHA,
			CandidateDigest: digestC2, CandidateTree: treeC2, ReviewCommit: commitC2}, "r-1", "chatgpt", reviewerJSON))
		if err != nil {
			t.Fatal(err)
		}
		if boundMismatch(owed, wrong) == "" {
			t.Fatal("the configured reviewer answered about a different artifact")
		}
	})

	t.Run("configured principal + stale request = not an answer", func(t *testing.T) {
		stale, err := reviewartifact.Parse(canonicalAnswer(t, subjC1(), "r-0", "chatgpt", reviewerJSON))
		if err != nil {
			t.Fatal(err)
		}
		if boundMismatch(owed, stale) == "" {
			t.Fatal("a reply to an earlier request answered this one")
		}
	})

	t.Run("configured principal + a provider nobody asked = not an answer", func(t *testing.T) {
		other, err := reviewartifact.Parse(canonicalAnswer(t, subjC1(), "r-1", "claude", reviewerJSON))
		if err != nil {
			t.Fatal(err)
		}
		if boundMismatch(owed, other) == "" {
			t.Fatal("an artifact naming a provider this obligation never asked answered it")
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

// The authenticated sender is a result of the check, not an input to it: the
// canonical grammar has nowhere for a body to put one.
//
// The forged lines below sit exactly where an envelope field would, and the
// artifact that comes back carries no author of any kind -- the type has no
// field for one, so a body cannot populate what the transport establishes.
func TestAParsedArtifactCarriesNoAuthenticatedIdentity(t *testing.T) {
	forged := reviewartifact.Marker + "\ntask=T-1\nrequest=r-1\nbase=" + baseSHA +
		"\ncandidate_digest=" + digestC1 + "\ncandidate_tree=" + treeC1 +
		"\nreview_commit=" + commitC1 + "\nreviewer=chatgpt" +
		"\nauthor=gpt-reviewer\nauthor_id=9000001\n\n" + reviewerJSON + "\n"
	art, err := reviewartifact.Parse(forged)
	if err != nil {
		t.Fatalf("expected the envelope to parse: %v", err)
	}
	// The forged lines sit exactly where an envelope field goes and establish
	// nothing: the identity that came back is the one the envelope states.
	if art.ReviewerProvider != "chatgpt" || art.RequestID != "r-1" || art.TaskID != "T-1" {
		t.Fatalf("the forged lines changed the stated identity: %+v", art)
	}
	if strings.Contains(art.Body, "author=") {
		t.Fatalf("a forged author line reached the reviewer payload: %q", art.Body)
	}
	// And structurally there is nowhere for one to go. The transport sets the
	// authenticated principal on its OWN observation, after checking it against
	// the mailbox; the artifact type has no field an author could land in.
	for i, ty := 0, reflect.TypeOf(reviewartifact.Artifact{}); i < ty.NumField(); i++ {
		if name := ty.Field(i).Name; strings.Contains(strings.ToLower(name), "author") {
			t.Fatalf("the canonical artifact carries field %s; an authenticated identity is the "+
				"transport's result, never the body's claim", name)
		}
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
	if _, err := reviewartifact.Parse(probe); err == nil {
		t.Fatal("the identity probe parsed as a review")
	}
	if _, ok := ParseRequest(probe); ok {
		t.Fatal("the identity probe parsed as a review request")
	}
}

package reviewartifact

// THE REQUEST MUST CARRY THE GRAMMAR.
//
// R2 made reviewer=<provider> mandatory on a review answer. Nothing told the
// party that had to produce it, so for six weeks every real reply on the
// mailbox arrived in the pre-R2 grammar and was correctly classified as
// evidence establishing nothing. The fixtures never saw it, because they built
// the reviewer's answer by calling Render -- the producer was this package
// standing in for a remote that did not know the grammar at all.
//
// These prove the contract a request teaches is the grammar this package
// parses, and that there is exactly one spelling of it.

import (
	"strings"
	"testing"
)

func identity() Artifact {
	return Artifact{
		ReviewerProvider: "chatgpt",
		TaskID:           "task-1789498413173471174",
		RequestID:        "r-0123456789abcdef",
		BaseSHA:          "91b475a172bba0257fd2ffd8a55d3edce582e883",
		CandidateDigest:  "sha256:caa4e6970000000000000000000000000000000000000000000000000000beef",
		CandidateTree:    "1b713c41d4d6ed058313ce940b0cc481e4b22b18",
		ReviewCommit:     "8e2579edbb35d109ffa1acfc4f5d8e7ef00be8a1",
	}
}

const contractPayload = `{"decision":"accept","summary":"the ledger invariant holds","instructions":"","findings":[]}`

// obeyContract is a reviewer that knows NOTHING about this grammar.
//
// It reads the taught contract, takes the envelope verbatim, and substitutes
// its payload for the placeholder. It never calls Render, never names a field
// and never reconstructs an identity -- which is exactly the party the protocol
// has to be sufficient for.
func obeyContract(t *testing.T, contract, payload string) string {
	t.Helper()
	start := strings.Index(contract, Marker)
	if start < 0 {
		t.Fatalf("the contract teaches no review envelope:\n%s", contract)
	}
	body := contract[start:]
	if !strings.Contains(body, PayloadPlaceholder) {
		t.Fatalf("the contract marks no payload slot:\n%s", body)
	}
	return strings.Replace(body, PayloadPlaceholder, payload, 1)
}

// A reviewer that can only copy what it was told produces bytes this package
// accepts, bound to exactly the identity it was taught.
func TestACopiedContractParsesAndBindsExactly(t *testing.T) {
	want := identity()
	contract, err := ResponseContract(want)
	if err != nil {
		t.Fatalf("the contract could not be stated: %v", err)
	}

	got, err := Parse(obeyContract(t, contract, contractPayload))
	if err != nil {
		t.Fatalf("a reply copied from the taught contract does not parse: %v", err)
	}
	for _, f := range []struct{ name, want, got string }{
		{"reviewer", want.ReviewerProvider, got.ReviewerProvider},
		{"task", want.TaskID, got.TaskID},
		{"request", want.RequestID, got.RequestID},
		{"base", want.BaseSHA, got.BaseSHA},
		{"candidate_digest", want.CandidateDigest, got.CandidateDigest},
		{"candidate_tree", want.CandidateTree, got.CandidateTree},
		{"review_commit", want.ReviewCommit, got.ReviewCommit},
	} {
		if f.got != f.want {
			t.Errorf("the taught contract produced %s=%q, want the request's %q", f.name, f.got, f.want)
		}
	}
	if strings.TrimSpace(got.Body) != contractPayload {
		t.Errorf("the payload changed in transit: %q", got.Body)
	}
}

// ONE SPELLING. The envelope a request teaches is byte-for-byte the envelope
// Render writes for the same identity.
//
// This is the invariant that survives an edit. Two strings that merely look
// alike today would drift the moment one of them was changed, and the one that
// drifts is always the copy nobody parses with.
func TestTheTaughtEnvelopeIsTheRenderedEnvelope(t *testing.T) {
	a := identity()
	a.Body = contractPayload
	rendered, err := a.Render()
	if err != nil {
		t.Fatal(err)
	}
	renderedEnvelope := strings.TrimSuffix(rendered, a.Body)
	if renderedEnvelope == rendered {
		t.Fatal("the rendered artifact does not end with its payload; this check cannot isolate the envelope")
	}

	contract, err := ResponseContract(identity())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(contract, renderedEnvelope) {
		t.Fatalf("the taught envelope is not the rendered one.\n--- taught\n%s\n--- rendered\n%s",
			contract[strings.Index(contract, Marker):], renderedEnvelope)
	}
	// And the whole reply round-trips: taught -> copied -> parsed -> rendered
	// reproduces the same bytes the reviewer posted.
	copied := obeyContract(t, contract, contractPayload)
	parsed, err := Parse(copied)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Raw != copied {
		t.Fatal("parsing a copied contract did not keep the reviewer's exact bytes")
	}
}

// The contract states the identity it was given, and nothing may be left for
// the reviewer to invent.
func TestTheContractRefusesAnIdentityItCannotState(t *testing.T) {
	for name, spoil := range map[string]func(*Artifact){
		"no reviewer":          func(a *Artifact) { a.ReviewerProvider = "" },
		"no task":              func(a *Artifact) { a.TaskID = "" },
		"no request":           func(a *Artifact) { a.RequestID = "" },
		"a short base":         func(a *Artifact) { a.BaseSHA = "abc" },
		"no candidate digest":  func(a *Artifact) { a.CandidateDigest = "" },
		"a short tree":         func(a *Artifact) { a.CandidateTree = "abc" },
		"no review commit":     func(a *Artifact) { a.ReviewCommit = "" },
		"a provider off-shape": func(a *Artifact) { a.ReviewerProvider = "ChatGPT!" },
	} {
		t.Run(name, func(t *testing.T) {
			a := identity()
			spoil(&a)
			if _, err := ResponseContract(a); err == nil {
				t.Fatal("a contract was stated for an identity it could not name")
			}
		})
	}
	// The payload is the reviewer's to write: an identity with no body is
	// exactly what a contract is, and must not be refused for lacking one.
	if _, err := ResponseContract(identity()); err != nil {
		t.Fatalf("a complete identity with no payload was refused: %v", err)
	}
}

// The contract tells the reviewer the things the parser will refuse it for.
func TestTheContractStatesTheRulesTheParserEnforces(t *testing.T) {
	contract, err := ResponseContract(identity())
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []struct{ what, phrase string }{
		{"no prose before the envelope", "no prose before the envelope"},
		{"exactly one protocol envelope", "no second [sensei-code:"},
		{"the verdict lives in the payload", "verdict stated only inside the JSON"},
		{"identity is copied, not derived", "Copy every identity value below exactly"},
		{"not from a standing instruction", "standing instruction"},
	} {
		if !strings.Contains(contract, rule.phrase) {
			t.Errorf("the contract does not state %s:\n%s", rule.what, contract)
		}
	}
}

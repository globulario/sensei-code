package ghbridge

import (
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/reviewartifact"
)

// The relay carries a review; it does not define one. These prove the adapter
// adds no grammar of its own: the same bytes mean the same thing here as they
// do to the canonical artifact, which is what lets a later slice ingest a
// mailbox review and a relayed review through one parser.
func TestTheRelayLiftsTheCanonicalArtifactWithoutReinterpretingIt(t *testing.T) {
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	canonical, err := reviewartifact.Parse(raw)
	if err != nil {
		t.Fatalf("the canonical parser refused the relay fixture: %v", err)
	}
	art, err := ParseRelayArtifact(raw)
	if err != nil {
		t.Fatalf("the relay parser refused an artifact the canonical parser accepted: %v", err)
	}
	for _, f := range []struct{ name, want, got string }{
		{"reviewer", canonical.ReviewerProvider, art.Provider},
		{"task", canonical.TaskID, art.TaskID},
		{"request", canonical.RequestID, art.RequestID},
		{"base", canonical.BaseSHA, art.BaseSHA},
		{"candidate_digest", canonical.CandidateDigest, art.CandidateDigest},
		{"candidate_tree", canonical.CandidateTree, art.CandidateTree},
		{"review_commit", canonical.ReviewCommit, art.ReviewCommit},
		{"body", canonical.Body, art.Body},
		{"raw", canonical.Raw, art.Raw},
		{"digest", canonical.Digest, art.Digest},
	} {
		if f.got != f.want {
			t.Errorf("the relay reports %s as %q; the canonical artifact says %q", f.name, f.got, f.want)
		}
	}
}

// The digest names the bytes the reviewer sent, not a tidied version of them.
//
// The fixture is padded with exactly the whitespace a normalizing adapter would
// drop. If the relay digested its own cleaned-up copy, the receipt, the
// published comment and any attestation covering that digest would all name an
// artifact nobody authored -- and an edit made in transit inside that padding
// would leave no trace.
func TestTheRelayDigestsTheExactBytesItWasGiven(t *testing.T) {
	padded := "\n  " + artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload) + "\n \n"
	art, err := ParseRelayArtifact(padded)
	if err != nil {
		t.Fatalf("an artifact with surrounding whitespace was refused: %v", err)
	}
	if art.Raw != padded {
		t.Fatalf("Raw is %q, want the exact submitted bytes %q", art.Raw, padded)
	}
	if want := reviewartifact.Digest(padded); art.Digest != want {
		t.Fatalf("Digest is %s, want the digest of the exact bytes %s", art.Digest, want)
	}
	if trimmed := reviewartifact.Digest(strings.TrimSpace(padded)); art.Digest == trimmed {
		t.Fatal("the relay digested a normalized copy; the digest must name the reviewer's own bytes")
	}
	if ReviewDigest(padded) != art.Digest {
		t.Fatalf("ReviewDigest(%d bytes) = %s, want %s", len(padded), ReviewDigest(padded), art.Digest)
	}
}

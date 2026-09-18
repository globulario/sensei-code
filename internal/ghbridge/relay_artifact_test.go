package ghbridge

import (
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/reviewartifact"
)

// The relay carries a review; it does not define one.
//
// Until R6 it lifted the canonical artifact into a relay-shaped type, and these
// proved the lift added no grammar. There is no lift any more -- the adapter
// calls reviewartifact.Parse and stores the bytes -- so what is left to prove is
// that nothing between the terminal and the durable record touches them.

// The digest names the bytes the reviewer sent, not a tidied version of them.
//
// The fixture is padded with exactly the whitespace a normalizing adapter would
// drop. If the relay staged its own cleaned-up copy, the record, the published
// comment and any attestation covering that digest would all name an artifact
// nobody authored -- and an edit made in transit inside that padding would leave
// no trace.
func TestTheRelayStoresTheExactBytesItWasGiven(t *testing.T) {
	f := newRelayFixture(t)
	padded := "\n  " + artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload) + "\n \n"
	if _, err := reviewartifact.Parse(padded); err != nil {
		t.Fatalf("the padded fixture is not a valid artifact, so this proves nothing: %v", err)
	}

	res, err := f.submit(padded)
	if err != nil {
		t.Fatalf("an artifact with surrounding whitespace was refused: %v", err)
	}
	rec, found := f.stored(t)
	if !found {
		t.Fatal("the padded artifact was not stored")
	}
	if rec.ArtifactRaw != padded {
		t.Fatalf("the stored artifact is %q, want the exact submitted bytes %q", rec.ArtifactRaw, padded)
	}
	if want := reviewartifact.Digest(padded); rec.ReviewDigest != want || res.ReviewDigest != want {
		t.Fatalf("digest record=%s result=%s, want the digest of the exact bytes %s",
			rec.ReviewDigest, res.ReviewDigest, want)
	}
	// The opposite direction: a normalizing adapter would produce THIS digest,
	// and every fixture without padding would agree with it.
	if trimmed := reviewartifact.Digest(strings.TrimSpace(padded)); rec.ReviewDigest == trimmed {
		t.Fatal("the relay stored a normalized copy; the digest must name the reviewer's own bytes")
	}
	// And the publication names the same bytes the record holds.
	posted := f.mailbox.posted()
	if len(posted) != 1 || !strings.Contains(posted[0], "review_digest="+rec.ReviewDigest) {
		t.Fatalf("the publication does not name the stored review %s:\n%v", rec.ReviewDigest, posted)
	}
}

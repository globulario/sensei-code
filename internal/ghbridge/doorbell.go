package ghbridge

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The doorbell exists because the remote actor's wake path and this protocol's
// authorship requirement are incompatible.
//
// Measured on 2026-09-07: a human-authored PR comment woke the remote consumer
// at 16:16:19Z and again at 17:14:33Z, while an App-authored architecture
// request at 17:05:00Z did not, and neither did an App-authored comment with no
// marker at all at 18:31:04Z. Actor was the only surviving variable — our own
// webhook received that last comment three seconds after it was posted, so
// GitHub emits the event and delivers it; the remote subscription is the only
// consumer that does not act on it.
//
// That leaves the design in a bind. The request MUST be App-authored, because a
// request authored by the operator would make asker and answerer the same
// principal and there would be nothing left for reviewer independence to mean.
// But an App-authored request is exactly what the wake path cannot see.
//
// So the two jobs the request was doing are separated. The REQUEST stays
// App-authored, fully bound, and is what a consumer validates. The WAKE becomes
// a different object: one marker and one number, posted by whoever the wake
// path will actually listen to.
//
// This is not the machine wearing the operator's identity. The distinction is
// what the two objects can DO:
//
//	request  task, objective digest, base, graph commit, candidate identity
//	         — everything a turn is bound to, and App-authored
//	wake     one comment id
//
// A wake carries no task, no digest, no base, no graph commit, no candidate,
// and no verdict. It says "look at comment N" and nothing else. Replaying one,
// forging one, or posting one by hand can therefore achieve nothing beyond
// asking a consumer to re-examine a comment that was already authenticated on
// its own terms — which is precisely what a doorbell should be able to mean.
//
// The type is deliberately incapable of carrying more. Ring takes an int64 and
// builds the body itself; there is no parameter through which arbitrary text
// could reach the conversation, so the doorbell cannot grow into a second
// channel for protocol content by being called differently.

// WakeMarker distinguishes a wake signal from every other comment. It is not a
// protocol turn: no consumer may read authority, identity or a verdict from it.
const WakeMarker = "[sensei-code:wake]"

const wakeField = "request_comment"

var wakeBody = regexp.MustCompile(`^\[sensei-code:wake\]\n` + wakeField + `=([0-9]{1,19})\n?$`)

// ErrNotAWake reports a body that is not a wake signal.
var ErrNotAWake = errors.New("not a sensei-code wake signal")

// Doorbell rings a wake signal pointing at an already-published request.
//
// Narrow on purpose, and the narrowness is the security property. The only
// argument is the id of a comment this process just published, so what a
// doorbell can express is bounded by what this interface can say — which is one
// number. Widening it to take a body, a task, or a binding would recreate the
// thing it exists to avoid: a second path by which content reaches the mailbox
// under an identity that is not the App's.
type Doorbell interface {
	Ring(ctx context.Context, requestCommentID int64) error
}

// RenderWake builds the one body a doorbell may post.
//
// Exported so a consumer and a test read the same shape this posts, rather than
// each re-deriving it from prose and agreeing only until one of them changes.
func RenderWake(requestCommentID int64) (string, error) {
	if requestCommentID <= 0 {
		return "", fmt.Errorf("a wake must point at a published comment, got id %d", requestCommentID)
	}
	return fmt.Sprintf("%s\n%s=%d\n", WakeMarker, wakeField, requestCommentID), nil
}

// ParseWake reads a wake signal and returns only the locator it carries.
//
// Strict by construction: the whole body must be the marker and exactly one
// numeric field. A wake with extra lines, extra fields, or a non-numeric
// locator is refused rather than partially read, because the one thing a
// consumer does with this value is fetch a comment by it — and a locator
// assembled from a body that also contained something else is a locator whose
// provenance nobody checked.
func ParseWake(body string) (int64, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(body), "\r\n", "\n") + "\n"
	m := wakeBody.FindStringSubmatch(normalized)
	if m == nil {
		return 0, ErrNotAWake
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrNotAWake
	}
	return id, nil
}

// GHDoorbell rings through the operator's gh credentials.
//
// A SEPARATE type from AppClient, never a branch inside it, and that separation
// is required rather than tidy. AppClient refuses to fall back to a person's
// credentials when its App transport is selected but unavailable — publishing
// machine-originated protocol traffic under someone's identity is the failure
// that refusal exists to prevent, and it is recorded as a forbidden fix. A
// doorbell posting under the operator's account is not that failure, because it
// publishes no protocol content; but if it lived inside AppClient the two would
// share a code path, and the next reader would have no way to see which of them
// they were extending.
type GHDoorbell struct {
	// Dir is the repository gh is invoked from.
	Dir string
	// Conversation is the PR whose top-level conversation is the mailbox. It is
	// configuration held here, never a Ring parameter: a doorbell that could be
	// aimed per call could point at a comment in one conversation while waking
	// another, and the mismatch would be invisible.
	Conversation string
}

// Ring implements Doorbell.
func (d GHDoorbell) Ring(ctx context.Context, requestCommentID int64) error {
	if d.Conversation == "" {
		return errors.New("the doorbell has no conversation to ring in")
	}
	body, err := RenderWake(requestCommentID)
	if err != nil {
		return err
	}
	out, err := run(ctx, d.Dir, []string{"issue", "comment", d.Conversation, "--body", body})
	if err != nil {
		return fmt.Errorf("ringing the doorbell for comment %d: %w: %s", requestCommentID, err, out)
	}
	return nil
}

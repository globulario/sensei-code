package ghbridge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
)

// A wake carries one locator and nothing else. These pin that the doorbell
// cannot become a second channel for protocol content.

func TestAWakeCarriesExactlyOneLocator(t *testing.T) {
	body, err := RenderWake(5573752546)
	if err != nil {
		t.Fatal(err)
	}
	if body != "[sensei-code:wake]\nrequest_comment=5573752546\n" {
		t.Fatalf("wake body = %q", body)
	}
	// Nothing a turn is bound to may appear in a wake.
	for _, forbidden := range []string{"task", "objective_digest", "base", "graph_build_commit",
		"candidate", "decision", "kind="} {
		if strings.Contains(body, forbidden) {
			t.Errorf("a wake carried %q; it must say only which comment to look at", forbidden)
		}
	}
	got, err := ParseWake(body)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if got != 5573752546 {
		t.Fatalf("locator = %d", got)
	}
}

func TestAWakeMustPointAtAPublishedComment(t *testing.T) {
	for _, bad := range []int64{0, -1} {
		if _, err := RenderWake(bad); err == nil {
			t.Errorf("RenderWake(%d) produced a wake pointing at nothing", bad)
		}
	}
}

// Strict by construction: a body that is a wake PLUS anything else is refused,
// because the one use of this value is fetching a comment by it.
func TestOnlyABareWakeIsReadAsAWake(t *testing.T) {
	for name, body := range map[string]string{
		"extra field":       "[sensei-code:wake]\nrequest_comment=1\ntask=t-1\n",
		"extra prose":       "[sensei-code:wake]\nrequest_comment=1\nplease hurry\n",
		"prose before":      "look at this\n[sensei-code:wake]\nrequest_comment=1\n",
		"no locator":        "[sensei-code:wake]\n",
		"non numeric":       "[sensei-code:wake]\nrequest_comment=abc\n",
		"wrong field":       "[sensei-code:wake]\ncomment=1\n",
		"architecture turn": "[sensei-code:architecture]\ntask=t\n\n{}",
		"empty":             "",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseWake(body); !errors.Is(err, ErrNotAWake) {
				t.Errorf("ParseWake accepted %q", body)
			}
		})
	}
}

func TestAWakeSurvivesCRLFAndSurroundingWhitespace(t *testing.T) {
	for _, body := range []string{
		"[sensei-code:wake]\r\nrequest_comment=42\r\n",
		"\n  [sensei-code:wake]\nrequest_comment=42\n  \n",
	} {
		got, err := ParseWake(body)
		if err != nil || got != 42 {
			t.Errorf("ParseWake(%q) = %d, %v", body, got, err)
		}
	}
}

// recordingDoorbell records rings and can be made to fail.
type recordingDoorbell struct {
	rung []int64
	err  error
}

func (d *recordingDoorbell) Ring(_ context.Context, id int64) error {
	d.rung = append(d.rung, id)
	return d.err
}

// The load-bearing property: a doorbell that fails does NOT cost a second
// request. The published request is durable, so failing the turn here would
// discard it and the next attempt would mint another for the same objective —
// a transport problem becoming a duplicate governed turn.
func TestAFailedRingDoesNotPublishASecondRequest(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)

	bell := &recordingDoorbell{err: errors.New("doorbell unreachable")}
	runner := &ArchitectureRunner{
		Issue:        box,
		Binding:      architectureBinding(),
		NewRequestID: NewRequestID,
		Poll:         10 * time.Millisecond,
		Wait:         120 * time.Millisecond,
		Doorbell:     bell,
	}

	var events []event.Event
	_, err := runner.Run(context.Background(),
		agent.Request{Role: roles.Architect, TaskID: architectureBinding().TaskID, Prompt: "p"},
		func(e event.Event) { events = append(events, e) })
	if err == nil {
		t.Fatal("an unanswered turn returned success")
	}

	posted := 0
	for _, c := range m.comments {
		if body, ok := c["body"].(string); ok && strings.HasPrefix(body, "[sensei-code:architecture-request]") {
			posted++
		}
	}
	if posted != 1 {
		t.Fatalf("%d architecture requests published; a doorbell failure must not mint another", posted)
	}
	if len(bell.rung) != 1 {
		t.Fatalf("doorbell rung %d time(s), want exactly one attempt", len(bell.rung))
	}

	// The locator must be reported, so a retry can ring the same comment.
	var reported bool
	for _, e := range events {
		if strings.Contains(string(e.Payload), `"request_comment"`) {
			reported = true
		}
	}
	if !reported {
		t.Error("a failed ring did not report the locator a retry must target")
	}
}

// The happy path rings exactly the comment that was just published.
func TestTheDoorbellRingsThePublishedRequest(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	_, box := newPRMailbox(t, keyPath, "157", true)

	bell := &recordingDoorbell{}
	runner := &ArchitectureRunner{
		Issue:        box,
		Binding:      architectureBinding(),
		NewRequestID: NewRequestID,
		Poll:         10 * time.Millisecond,
		Wait:         120 * time.Millisecond,
		Doorbell:     bell,
	}
	_, _ = runner.Run(context.Background(),
		agent.Request{Role: roles.Architect, TaskID: architectureBinding().TaskID, Prompt: "p"}, nil)

	if len(bell.rung) != 1 {
		t.Fatalf("doorbell rung %d time(s), want one", len(bell.rung))
	}
	if bell.rung[0] <= 0 {
		t.Fatalf("doorbell rang locator %d; it must point at the published comment", bell.rung[0])
	}
}

// No doorbell configured is the arrangement wherever the wake path admits the
// App. It must not become an error, and nothing may be posted in its place.
func TestNoDoorbellIsNotAFailure(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)

	runner := &ArchitectureRunner{
		Issue: box, Binding: architectureBinding(), NewRequestID: NewRequestID,
		Poll: 10 * time.Millisecond, Wait: 120 * time.Millisecond,
	}
	_, _ = runner.Run(context.Background(),
		agent.Request{Role: roles.Architect, TaskID: architectureBinding().TaskID, Prompt: "p"}, nil)

	for _, c := range m.comments {
		if body, ok := c["body"].(string); ok && strings.HasPrefix(body, WakeMarker) {
			t.Error("a wake was posted with no doorbell configured")
		}
	}
}

// The doorbell is not a fallback inside the App transport. GHDoorbell is its
// own type, and AppClient exposes no way to ring one.
func TestTheDoorbellIsSeparateFromTheAppTransport(t *testing.T) {
	var _ Doorbell = GHDoorbell{}
	if _, ok := any(&AppClient{}).(Doorbell); ok {
		t.Error("AppClient satisfies Doorbell; the operator-credential wake path must not " +
			"share a type with the App transport that refuses to fall back to it")
	}
}

func TestADoorbellWithNoConversationRefuses(t *testing.T) {
	if err := (GHDoorbell{Dir: t.TempDir()}).Ring(context.Background(), 42); err == nil {
		t.Error("a doorbell with no conversation rang anyway")
	}
}

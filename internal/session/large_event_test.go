package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/event"
)

// THE WRITER MUST NOT BE ABLE TO CREATE A RECORD THE READER CANNOT OPEN.
//
// Append bounds nothing; Load used bufio.Scanner's default 64 KiB token ceiling. So
// Sensei-Code could durably record an event it could never read back -- a poison pill
// of its own making, and one that got MORE likely as the work got more substantial,
// because the oversized event is the candidate diff.
//
// Observed 2026-09-17: a 172_623-byte candidate.changed event on a 3_236-line
// candidate made `sensei-code resume` and `resume --list` fail with
// "bufio.Scanner: token too long", which stranded a validated, audited candidate
// whose review was owed.

// bigEvent is one event whose payload is comfortably past the old ceiling.
func bigEvent(taskID string, n int) event.Event {
	return event.Event{
		TaskID:  taskID,
		Source:  event.SourceSystem,
		Kind:    event.CandidateChanged,
		Summary: "candidate diff",
		Payload: json.RawMessage(`{"diff":"` + strings.Repeat("x", n) + `"}`),
	}
}

// 1. A valid event past the default ceiling survives the round trip.
func TestAnEventLargerThanTheDefaultCeilingSurvivesTheRoundTrip(t *testing.T) {
	const size = 200_000 // > 64 KiB, and larger than the 172_623 observed in the wild
	store, err := New(t.TempDir(), "session-large")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Append(bigEvent("t1", size)); err != nil {
		t.Fatalf("Append refused an event it is expected to accept: %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Append wrote an event Load cannot read back; the writer created a record "+
			"the reader cannot open: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("loaded %d events, want 1", len(got))
	}
	// Byte-for-byte where it matters: a reader that silently shortened the payload
	// would satisfy "no error" and lose the evidence.
	var decoded struct {
		Diff string `json:"diff"`
	}
	if err := json.Unmarshal(got[0].Payload, &decoded); err != nil {
		t.Fatalf("the payload did not survive as valid JSON: %v", err)
	}
	if len(decoded.Diff) != size {
		t.Fatalf("payload came back %d bytes, want %d: the event was truncated, not read", len(decoded.Diff), size)
	}
}

// 2. THE LIFECYCLE, not the scanner. This is the failure that actually cost us: the
// oversized event made the whole record unreadable, so nothing downstream could
// derive resumable state and a preserved candidate became unreachable.
//
// A test that only proved "Scanner reads 200 KB" would fix the byte limit and miss
// this.
func TestAPreservedCandidateStaysResumableAcrossALargeEvent(t *testing.T) {
	store, err := New(t.TempDir(), "session-lifecycle")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	for _, e := range []event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "establish the census"),
		ev("t1", event.SourceArchitect, event.PlanProposed, "the plan"),
		bigEvent("t1", 200_000), // the candidate diff that used to poison the record
		ev("t1", event.SourceReviewer, event.WorkflowAwaitingReview, "preserved awaiting review"),
	} {
		if err := store.Append(e); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	events, err := store.Load()
	if err != nil {
		t.Fatalf("the durable record became unreadable, so resume/list cannot reconstruct state: %v", err)
	}
	got := FindInterrupted(events)
	if len(got) != 1 {
		t.Fatalf("found %d resumable tasks, want 1; a large event severed the lifecycle", len(got))
	}
	if !got[0].AwaitingReview {
		t.Fatalf("the preserved candidate is no longer recognised as awaiting review: %+v", got[0])
	}
	if got[0].Task != "establish the census" || got[0].Plan != "the plan" {
		t.Fatalf("state before the large event was lost: %+v", got[0])
	}
}

// 3. Ordinary small events are unaffected.
func TestOrdinarySmallEventsStillLoad(t *testing.T) {
	store, err := New(t.TempDir(), "session-small")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	for i, e := range []event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "small task"),
		ev("t1", event.SourceArchitect, event.PlanProposed, "small plan"),
	} {
		if err := store.Append(e); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("small events no longer load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("loaded %d events, want 2", len(got))
	}
	if got[0].Summary != "small task" || got[1].Summary != "small plan" {
		t.Fatalf("ordinary events came back wrong: %+v", got)
	}
}

// 4. THE CEILING REMAINS FINITE, AND CROSSING IT IS AN ERROR.
//
// Unbounded would trade one defect for a worse one: a corrupt or hostile record could
// exhaust memory. So the bound stays, and the test that it is a REFUSAL rather than a
// truncation is what makes the bound safe -- a reader that quietly returned the first
// 16 MiB of a larger event would report success on evidence that is not what was
// written.
func TestCrossingTheMaximumIsRefusedRatherThanTruncated(t *testing.T) {
	store, err := New(t.TempDir(), "session-over")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Append(bigEvent("t1", maxSessionEvent+1024)); err != nil {
		t.Fatalf("append: %v", err)
	}

	got, loadErr := store.Load()
	if loadErr == nil {
		t.Fatalf("an event past the maximum loaded anyway; %d event(s) returned. Either the bound is "+
			"gone or the event was truncated, and a truncated event is not the event that was written", len(got))
	}
	if !errors.Is(loadErr, bufio.ErrTooLong) {
		t.Errorf("the refusal does not identify itself as the size bound: %v", loadErr)
	}
	if !strings.Contains(loadErr.Error(), "refused rather than truncated") {
		t.Errorf("the error does not say the event was refused rather than shortened: %v", loadErr)
	}
	if got != nil {
		t.Errorf("a refused load returned %d event(s); it must return nothing rather than a partial record", len(got))
	}
}

// THE BOUND MUST STAY FINITE AND SANE, and that cannot be witnessed by a test whose
// oversized fixture is defined as maxSessionEvent+n.
//
// Found by mutation: raising the constant to 1<<40 left every other test in this file
// "passing" -- the refusal test simply tried to allocate a terabyte and died of OOM,
// which is a crash, not a verdict. A test expressed in terms of the bound cannot
// detect a change to the bound. So this one asserts the bound ABSOLUTELY.
//
// The upper limit is not a style preference. An effectively unbounded reader lets a
// corrupt or hostile record dictate the allocation, which trades a readability defect
// for a denial-of-service one.
func TestTheMaximumEventSizeStaysFiniteAndSane(t *testing.T) {
	const observedInTheWild = 172_623 // the candidate.changed event that started this
	if maxSessionEvent <= observedInTheWild {
		t.Fatalf("maxSessionEvent is %d, which cannot read the %d-byte event that was actually "+
			"produced; the reader would still refuse a record the writer can create",
			maxSessionEvent, observedInTheWild)
	}
	// 16 MiB is the repository's authoritative limit for one governed event carrying a
	// whole candidate diff (runreceipt/legacy.maxLine, provider/codex_appserver.go).
	// A larger value here would be a second, competing size policy.
	const authoritative = 16 << 20
	if maxSessionEvent != authoritative {
		t.Errorf("maxSessionEvent is %d; the repository already treats %d as the limit for one "+
			"governed event, and a competing size policy is how two readers disagree about the "+
			"same record", maxSessionEvent, authoritative)
	}
	if initialSessionEventBuffer > maxSessionEvent {
		t.Errorf("the initial buffer (%d) exceeds the ceiling (%d), so ordinary events pay for the "+
			"rare one", initialSessionEventBuffer, maxSessionEvent)
	}
}

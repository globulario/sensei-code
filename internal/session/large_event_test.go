package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
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

// 4. THE REFUSAL HAPPENS AT APPEND, SO DURABLE HISTORY NEVER HOLDS AN UNREADABLE EVENT.
//
// Bounding only the reader moved the poison pill from 64 KiB to 16 MiB. An Append that
// succeeds while the matching Load refuses is the same contradiction at a higher
// threshold, and by the time Load refuses the bad record is already durable.
//
// The repaired contract is symmetric:
//
//	<= max   Append succeeds  ->  Load succeeds
//	 > max   Append refuses   ->  the existing session stays readable and unchanged
func TestAnOversizedEventIsRefusedBeforeItReachesDurableHistory(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, "session-refuse")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	// A real record exists first: the refusal must not cost what was already written.
	if err := store.Append(ev("t1", event.SourceSystem, event.TaskCreated, "establish the census")); err != nil {
		t.Fatalf("append: %v", err)
	}
	before, err := store.Load()
	if err != nil {
		t.Fatalf("baseline load: %v", err)
	}
	sizeBefore := recordSize(t, store)

	appendErr := store.Append(bigEvent("t1", maxSessionEvent+1024))
	if appendErr == nil {
		t.Fatal("Append accepted an event larger than the maximum; the writer can still create a " +
			"durable record the reader cannot open")
	}
	if !strings.Contains(appendErr.Error(), "refused before it is written") {
		t.Errorf("the refusal does not say it happened before the write: %v", appendErr)
	}

	// The session is untouched: no partial line, nothing lost, still readable.
	if got := recordSize(t, store); got != sizeBefore {
		t.Errorf("the refused append changed the record from %d to %d bytes", sizeBefore, got)
	}
	after, err := store.Load()
	if err != nil {
		t.Fatalf("a refused append left the session unreadable: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("the record holds %d events after a refused append, was %d", len(after), len(before))
	}
	if after[0].Summary != "establish the census" {
		t.Errorf("the surviving event changed: %+v", after[0])
	}
}

// THE BOUNDARY, AT THE EXACT BYTE.
//
// The largest event Append ACCEPTS must be one Load can read. That is a one-byte risk,
// not a theoretical one: Encode appends a newline, and Scanner's ceiling applies to the
// token excluding that delimiter. Disagree by one and the exact-maximum event is
// accepted and then unreadable -- the original defect surviving at a single size.
//
// An earlier version of this witness walked payload sizes in 64-byte steps and never
// landed on the edge; a mutation that let Append accept one byte too many SURVIVED it.
// A boundary test that samples a grid is not a boundary test. This one computes the
// encoding overhead and sizes the payload so the TOKEN is exactly the maximum, then
// exactly one over.
func TestTheLargestAcceptedEventIsReadableBack(t *testing.T) {
	overhead := encodingOverhead(t)

	atMax := bigEvent("t1", maxSessionEvent-overhead)
	if got := tokenLen(t, atMax); got != maxSessionEvent {
		t.Fatalf("fixture is off: token is %d, want exactly %d; this witness would not be testing "+
			"the boundary", got, maxSessionEvent)
	}
	oneOver := bigEvent("t1", maxSessionEvent-overhead+1)
	if got := tokenLen(t, oneOver); got != maxSessionEvent+1 {
		t.Fatalf("fixture is off: token is %d, want exactly %d", got, maxSessionEvent+1)
	}

	store, err := New(t.TempDir(), "session-boundary")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	// Exactly at the maximum: accepted, and READABLE BACK. This is the pair that must
	// agree; either half alone proves nothing.
	if err := store.Append(atMax); err != nil {
		t.Fatalf("Append refused an event whose token is exactly the maximum (%d): %v", maxSessionEvent, err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("an event Append ACCEPTED at exactly the maximum could not be read back; the writer "+
			"and reader disagree by at least one byte: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("loaded %d events, want 1", len(got))
	}

	// Exactly one over: refused, and the record left readable.
	if err := store.Append(oneOver); err == nil {
		t.Fatal("Append accepted an event one byte past the maximum; Load will refuse the record it " +
			"just wrote")
	}
	after, err := store.Load()
	if err != nil {
		t.Fatalf("the refused one-over append left the record unreadable: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("the record holds %d events after a refused append, want 1", len(after))
	}
}

// encodingOverhead is the difference between an event's encoded token and its payload
// length, measured from a real encode rather than counted by hand.
func encodingOverhead(t *testing.T) int {
	t.Helper()
	const probe = 1024
	return tokenLen(t, bigEvent("t1", probe)) - probe
}

// tokenLen is the encoded length Scanner will see: the line without its newline.
func tokenLen(t *testing.T, e event.Event) int {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(e); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Len() - 1
}

func recordSize(t *testing.T, s *Store) int64 {
	t.Helper()
	fi, err := os.Stat(s.path)
	if err != nil {
		t.Fatalf("stat record: %v", err)
	}
	return fi.Size()
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

// LOAD'S REFUSAL STILL HAS TO WORK, EVEN THOUGH APPEND NOW PREVENTS THE CASE.
//
// Once Append refuses oversized events, no record written by THIS binary can trip
// Load's ceiling -- which made a mutation that swallowed ErrTooLong and returned a
// partial record survive every other witness here. That is not proof the reader's
// refusal is unnecessary. A record can predate the Append bound, be written by an
// older binary, or be corrupted outside the process entirely, and a reader that
// silently returns a truncated prefix of such a record reports success on evidence
// that is not what was written.
//
// So this writes the oversized line directly, bypassing Append, which is exactly how
// the real case arises.
func TestLoadRefusesAnOversizedRecordItDidNotWrite(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, "session-foreign")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Append(ev("t1", event.SourceSystem, event.TaskCreated, "a real event")); err != nil {
		t.Fatalf("append: %v", err)
	}

	// A line no current Append would produce, appended behind its back.
	f, err := os.OpenFile(store.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open record: %v", err)
	}
	line := `{"task_id":"t1","summary":"` + strings.Repeat("x", maxSessionEvent+4096) + `"}` + "\n"
	if _, err := f.WriteString(line); err != nil {
		f.Close()
		t.Fatalf("write oversized line: %v", err)
	}
	f.Close()

	got, loadErr := store.Load()
	if loadErr == nil {
		t.Fatalf("Load accepted a record holding an oversized line and returned %d event(s); a "+
			"truncated prefix is not the record that was written", len(got))
	}
	if !errors.Is(loadErr, bufio.ErrTooLong) {
		t.Errorf("the refusal does not identify itself as the size bound: %v", loadErr)
	}
	if got != nil {
		t.Errorf("a refused load returned %d event(s); it must return nothing rather than a partial record", len(got))
	}
}

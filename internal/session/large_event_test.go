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

// largeSession is the session every governed record of these witnesses is
// written under: the Store takes a task's records only from the session its
// TaskCreated root makes current (70B2a1), so the fixtures root the task first.
const largeSession = "session-large"

// under is ev written by largeSession, with the event ID and timestamp every
// recorded event carries: a root without them proves no holder (validTaskRoot).
func under(e event.Event) event.Event {
	framed := event.New(largeSession, e.TaskID, e.Source, e.Kind, e.Summary, nil)
	e.SessionID, e.ID, e.Time = largeSession, framed.ID, framed.Time
	return e
}

// owesReview is e carrying the complete durable shape of an owed review,
// which the Store requires of a WorkflowAwaitingReview record (70B2a1).
func owesReview(e event.Event) event.Event {
	e.Payload = json.RawMessage(`{"review_kind":"advisory","independent_review":false,"obligations":["an independent review"]}`)
	return e
}

// bigFrame is the one event ID and timestamp every bigEvent carries, so a
// bigEvent's encoded length depends on its payload alone.
var bigFrame = event.New(largeSession, "", event.SourceSystem, event.CandidateChanged, "", nil)

// framedAs is e carrying frame's event ID and timestamp.
func framedAs(frame, e event.Event) event.Event {
	e.ID, e.Time = frame.ID, frame.Time
	return e
}

// bigEvent is one framed event whose payload is comfortably past the old
// ceiling: a run receipt carrying the whole candidate diff, in the closed
// shape the Store requires of a receipt record (70B2a1).
func bigEvent(taskID string, n int) event.Event {
	return framedAs(bigFrame, event.Event{
		SessionID: largeSession,
		TaskID:    taskID,
		Source:    event.SourceSystem,
		Kind:      event.RunReceipt,
		Summary:   "candidate diff",
		Payload:   json.RawMessage(`{"receipt":{"diff":"` + strings.Repeat("x", n) + `"},"completeness":"complete","missing":[]}`),
	})
}

// 1. A valid event past the default ceiling survives the round trip.
func TestAnEventLargerThanTheDefaultCeilingSurvivesTheRoundTrip(t *testing.T) {
	const size = 200_000 // > 64 KiB, and larger than the 172_623 observed in the wild
	store, err := New(t.TempDir(), "session-large")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := appendTip(t, store, t.Context(), false, under(ev("t1", event.SourceSystem, event.TaskCreated, "the task"))); err != nil {
		t.Fatalf("root: %v", err)
	}
	if err := appendTip(t, store, t.Context(), false, bigEvent("t1", size)); err != nil {
		t.Fatalf("Append refused an event it is expected to accept: %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Append wrote an event Load cannot read back; the writer created a record "+
			"the reader cannot open: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("loaded %d events, want the root and the large event", len(got))
	}
	// Byte-for-byte where it matters: a reader that silently shortened the payload
	// would satisfy "no error" and lose the evidence.
	var decoded struct {
		Receipt struct {
			Diff string `json:"diff"`
		} `json:"receipt"`
	}
	if err := json.Unmarshal(got[1].Payload, &decoded); err != nil {
		t.Fatalf("the payload did not survive as valid JSON: %v", err)
	}
	if len(decoded.Receipt.Diff) != size {
		t.Fatalf("payload came back %d bytes, want %d: the event was truncated, not read", len(decoded.Receipt.Diff), size)
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
		under(ev("t1", event.SourceSystem, event.TaskCreated, "establish the census")),
		planned(under(ev("t1", event.SourceArchitect, event.PlanProposed, "the plan"))),
		bigEvent("t1", 200_000), // the candidate diff that used to poison the record
		owesReview(under(ev("t1", event.SourceReviewer, event.WorkflowAwaitingReview, "preserved awaiting review"))),
	} {
		if err := appendTip(t, store, t.Context(), false, e); err != nil {
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
		under(ev("t1", event.SourceSystem, event.TaskCreated, "small task")),
		planned(under(ev("t1", event.SourceArchitect, event.PlanProposed, "small plan"))),
	} {
		if err := appendTip(t, store, t.Context(), false, e); err != nil {
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
	if err := appendTip(t, store, t.Context(), false, under(ev("t1", event.SourceSystem, event.TaskCreated, "establish the census"))); err != nil {
		t.Fatalf("append: %v", err)
	}
	before, err := store.Load()
	if err != nil {
		t.Fatalf("baseline load: %v", err)
	}
	sizeBefore := recordSize(t, store)

	appendErr := appendTip(t, store, t.Context(), false, bigEvent("t1", maxSessionEvent+1024))
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
	if err := appendTip(t, store, t.Context(), false, under(ev("t1", event.SourceSystem, event.TaskCreated, "the task"))); err != nil {
		t.Fatalf("root: %v", err)
	}
	// Exactly at the maximum: accepted, and READABLE BACK. This is the pair that must
	// agree; either half alone proves nothing.
	if err := appendTip(t, store, t.Context(), false, atMax); err != nil {
		t.Fatalf("Append refused an event whose token is exactly the maximum (%d): %v", maxSessionEvent, err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("an event Append ACCEPTED at exactly the maximum could not be read back; the writer "+
			"and reader disagree by at least one byte: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("loaded %d events, want the root and the event at the maximum", len(got))
	}

	// Exactly one over: refused, and the record left readable.
	if err := appendTip(t, store, t.Context(), false, oneOver); err == nil {
		t.Fatal("Append accepted an event one byte past the maximum; Load will refuse the record it " +
			"just wrote")
	}
	after, err := store.Load()
	if err != nil {
		t.Fatalf("the refused one-over append left the record unreadable: %v", err)
	}
	if len(after) != 2 {
		t.Fatalf("the record holds %d events after a refused append, want 2", len(after))
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
	if err := appendTip(t, store, t.Context(), false, under(ev("t1", event.SourceSystem, event.TaskCreated, "a real event"))); err != nil {
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

// planned gives a plan.proposed fixture the payload every operative plan
// transition carries (session.governedKinds).
func planned(e event.Event) event.Event {
	e.Payload = json.RawMessage(`{"decision":"proceed","summary":"","plan":"","plan_source":"architect"}`)
	return e
}

// d5Prefix is the complete prefix every tail case below is appended to: the
// root of the claimed task, by the torn record's session.
func d5Prefix(t *testing.T, repo string) *Store {
	t.Helper()
	s := otherStore(t, repo, d5Torn)
	writeRaw(t, s, event.New(d5Torn, d5Claimed, event.SourceSystem, event.TaskCreated, "the objective", nil))
	return s
}

// failsClosed requires the repository to refuse an unrelated task and its
// discovery, as an unreadable record always made it.
func failsClosed(t *testing.T, repo, name string, want error) {
	t.Helper()
	if err := createTask(t, otherStore(t, repo, "S-new"), d5Unrelated); !errors.Is(err, want) {
		t.Fatalf("%s: an unrelated task was not refused (%v): %v", name, want, err)
	}
	if _, err := FindActive(repo); !errors.Is(err, want) {
		t.Fatalf("%s: discovery did not fail closed (%v): %v", name, want, err)
	}
}

// D5-W3: unknown claims stay fail-closed. With no manifest a torn record
// blocks as it always did; and a record whose damage is not a bounded,
// understood torn tail hiding no claim -- an illegible, unknown, repeated or
// non-string kind, a possible hidden TaskCreated, an illegible task identity,
// a malformed or complete-but-undecodable segment, an ambiguous or corrupt
// record -- is refused activation and publishes nothing. The inspection is
// structural: a kind nested in the payload is not the event's kind.
func TestD5W3UnknownClaimsRemainFailClosed(t *testing.T) {
	repo := t.TempDir()
	tornStore(t, repo, d5Torn, d5Claimed)
	failsClosed(t, repo, "no manifest", ErrRecordUnreadable)

	refused := map[string]string{
		"unknown kind":           `{"id":"x","task_id":"task-t","kind":"mystery","payload":{"a`,
		"truncated kind":         `{"id":"x","task_id":"task-t","kind":"outp`,
		"no kind":                `{"id":"x","task_id":"task-t","pay`,
		"kind nested only":       `{"task_id":"task-t","payload":{"kind":"output"},"pay`,
		"non-string kind":        `{"task_id":"task-t","kind":{"a":1},"x`,
		"kind repeated by case":  `{"kind":"output","task_id":"task-t","KIND":"task.created","pay`,
		"kind repeated escaped":  `{"kind":"output","task_id":"task-t","\u006bind":"task.created","pay`,
		"hidden root, cut id":    `{"id":"x","kind":"task.created","task_id":"task-hid`,
		"hidden root, no id":     `{"id":"x","kind":"task.created","summ`,
		"illegible task id":      `{"kind":"output","task_id":"task-d5-cl`,
		"task id repeated":       `{"kind":"output","task_id":"task-t","TASK_ID":"task-u","pay`,
		"malformed":              `{"kind":"output",,"task_id`,
		"not an object":          `["kind","output"`,
		"complete, undecodable":  `{"kind":"output","task_id":"task-t","time":"not a time"}`,
		"ambiguous":              string(mustMarshal(t, d5Output(d5Torn, d5Claimed))),
		"corrupt terminated":     "{\"kind\":\"output\"\n" + `{"kind":"output","task_id":"task-t","pay`,
		"unterminated blank":     "   ",
		"terminated blank first": "\n" + `{"kind":"output","task_id":"task-t","pay`,
		// f2: a top-level member name cut before it is complete may still be
		// a further kind or task identity, however legible the earlier ones.
		"subsequent kind key cut":    `{"kind":"output","task_id":"task-t","ki`,
		"subsequent task id key cut": `{"kind":"output","task_id":"task-t","task_`,
		"subsequent key just opened": `{"kind":"output","task_id":"task-t","`,
		"subsequent key folded cut":  `{"kind":"output","task_id":"task-t","\u212a`,
		"subsequent key escaped cut": `{"kind":"output","task_id":"task-t","\u006b`,
		"subsequent key cut in rune": "{\"kind\":\"output\",\"task_id\":\"task-t\",\"ta\xc5",
		"first key cut":              `{"ki`,
		// f1: a tail identity the decoder would read as another identity.
		"tail id invalid UTF-8":      "{\"kind\":\"output\",\"task_id\":\"task-t\xff\",\"pay",
		"tail id unpaired surrogate": `{"kind":"output","task_id":"task-t\ud800","pay`,
	}
	for name, tail := range refused {
		repo := t.TempDir()
		s := d5Prefix(t, repo)
		writeTail(t, s, tail)
		before := fileBytes(t, s.path)
		if _, err := ActivateQuarantine(t.Context(), repo, d5Torn, authorized(t, repo, d5Torn)); !errors.Is(err, ErrQuarantineRefused) {
			t.Fatalf("%s: activation was not refused: %v", name, err)
		}
		if exists(t, quarantinePath(s.path)) || !bytes.Equal(fileBytes(t, s.path), before) {
			t.Fatalf("%s: a refused activation published a manifest or changed the record", name)
		}
		if in, err := InspectQuarantine(repo, d5Torn); err != nil || in.State != QuarantineIneligible {
			t.Fatalf("%s: inspected as %+v (%v), not ineligible", name, in, err)
		}
		failsClosed(t, repo, name, ErrRecordUnreadable)
	}

	// Structural, not textual: a TaskCreated named inside the payload is not
	// a claim, and the legible task identity is reserved conservatively.
	repo = t.TempDir()
	s := d5Prefix(t, repo)
	writeTail(t, s, `{"kind":"output","task_id":"task-d5-tail","payload":{"kind":"task.created","task_id":"task-d5-nested","x`)
	m := activate(t, repo, d5Torn)
	if strings.Join(m.Claims, ",") != d5Claimed+",task-d5-tail" || m.Tail.Kind != "output" || m.Tail.TaskID != "task-d5-tail" {
		t.Fatalf("the claims are %q and the tail %+v", m.Claims, m.Tail)
	}
	other := otherStore(t, repo, "S-new")
	if err := createTask(t, other, "task-d5-tail"); !errors.Is(err, ErrTaskQuarantined) {
		t.Fatalf("the tail's legible task identity was not reserved: %v", err)
	}
	if err := createTask(t, other, "task-d5-nested"); err != nil {
		t.Fatalf("an identity named only inside the payload was reserved: %v", err)
	}
}

// D5-W3: a TaskCreated root cannot hide in the complete prefix either. Each
// terminated line below decodes, but not as what it structurally names: a
// kind overwritten by a later, case-folded or escaped member, a repeated or
// non-string task identity, an unregistered kind, or a root whose identity
// is not canonical -- an empty or absent one included. Beside a torn tail that is itself understood, the record
// is still refused activation, publishes nothing and fails closed, so the
// identity "task-d5-hidden" is never released by a quarantine.
func TestD5W3AHiddenPrefixClaimRefusesActivation(t *testing.T) {
	const head = `{"id":"x","time":"2026-10-08T00:00:00Z","session_id":"S-torn","source":"system",`
	hidden := map[string]string{
		"kind overwritten":       head + `"task_id":"task-d5-hidden","kind":"task.created","kind":"output"}`,
		"kind case-folded":       head + `"task_id":"task-d5-hidden","KIND":"task.created","kind":"output"}`,
		"kind folded alone":      head + `"task_id":"task-d5-hidden","Kind":"task.created"}`,
		"kind escaped":           head + `"task_id":"task-d5-hidden","\u006bind":"task.created","kind":"output"}`,
		"task id overwritten":    head + `"kind":"task.created","task_id":"task-d5-hidden","task_id":""}`,
		"task id case-folded":    head + `"kind":"task.created","Task_ID":"task-d5-hidden"}`,
		"task id null":           head + `"kind":"task.created","task_id":null}`,
		"unregistered kind":      head + `"task_id":"task-d5-hidden","kind":"mystery"}`,
		"noncanonical root":      head + `"task_id":" task-d5-hidden","kind":"task.created"}`,
		"control character root": head + `"task_id":"task-d5-hidden\u0001","kind":"task.created"}`,
		"empty root":             head + `"task_id":"","kind":"task.created"}`,
		"absent root":            head + `"kind":"task.created"}`,
		// f1: raw bytes the decoder would replace with U+FFFD.
		"invalid UTF-8 root":      head + "\"task_id\":\"task-d5-hidden\xff\",\"kind\":\"task.created\"}",
		"unpaired surrogate root": head + `"task_id":"task-d5-hidden\udc00","kind":"task.created"}`,
		"invalid UTF-8 output id": head + "\"task_id\":\"task-d5-hidden\xff\",\"kind\":\"output\",\"payload\":{\"stream\":\"assistant\"}}",
	}
	for name, line := range hidden {
		repo := t.TempDir()
		s := d5Prefix(t, repo)
		writeTail(t, s, line+"\n"+`{"kind":"output","task_id":"`+d5Claimed+`","pay`)
		before := fileBytes(t, s.path)
		if sc := classifyRecord(before); sc.class != RecordTorn || sc.quarantinable() {
			t.Fatalf("%s: the record classified as %s, quarantinable %v", name, sc.class, sc.quarantinable())
		}
		if _, err := ActivateQuarantine(t.Context(), repo, d5Torn, authorized(t, repo, d5Torn)); !errors.Is(err, ErrQuarantineRefused) {
			t.Fatalf("%s: activation was not refused: %v", name, err)
		}
		if exists(t, quarantinePath(s.path)) || !bytes.Equal(fileBytes(t, s.path), before) {
			t.Fatalf("%s: a refused activation published a manifest or changed the record", name)
		}
		if in, err := InspectQuarantine(repo, d5Torn); err != nil || in.State != QuarantineIneligible {
			t.Fatalf("%s: inspected as %+v (%v), not ineligible", name, in, err)
		}
		failsClosed(t, repo, name, ErrRecordUnreadable)
		if err := createTask(t, otherStore(t, repo, "S-hidden"), "task-d5-hidden"); err == nil {
			t.Fatalf("%s: the identity the prefix may claim was admitted", name)
		}
	}

	// The control: the same prefix line, unambiguous, is classified and its
	// root reserved by the quarantine.
	repo := t.TempDir()
	s := d5Prefix(t, repo)
	writeTail(t, s, head+`"task_id":"task-d5-hidden","kind":"task.created"}`+"\n"+`{"kind":"output","task_id":"`+d5Claimed+`","pay`)
	m := activate(t, repo, d5Torn)
	if strings.Join(m.Claims, ",") != d5Claimed+",task-d5-hidden" {
		t.Fatalf("the unambiguous prefix root was not claimed: %q", m.Claims)
	}
	if err := createTask(t, otherStore(t, repo, "S-hidden"), "task-d5-hidden"); !errors.Is(err, ErrTaskQuarantined) {
		t.Fatalf("the prefix root was not reserved: %v", err)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// D5-W3 (review f1): a torn record whose complete prefix creates one
// identity twice is never quarantinable. Its tail is understood and its
// claims are all legible, but the duplicate leaves its events unattributable,
// so activation is refused and publishes nothing, a manifest forged from its
// own classification is invalid, and the record fails closed: a quarantine
// never erases the duplicate condition from the inventory.
func TestD5W3CADuplicateRootTornRecordIsNeverQuarantined(t *testing.T) {
	repo := t.TempDir()
	s := d5Prefix(t, repo)
	writeRaw(t, s, event.New(d5Torn, d5Claimed, event.SourceSystem, event.TaskCreated, "again", nil))
	writeTail(t, s, `{"kind":"output","task_id":"`+d5Claimed+`","pay`)
	before := fileBytes(t, s.path)
	if sc := classifyRecord(before); sc.class != RecordTorn || sc.quarantinable() || !errors.Is(sc.claimErr, ErrDuplicateTaskRoot) {
		t.Fatalf("the doubled torn record classified as %s, quarantinable %v: %v", sc.class, sc.quarantinable(), sc.claimErr)
	}
	if _, err := ActivateQuarantine(t.Context(), repo, d5Torn, authorized(t, repo, d5Torn)); !errors.Is(err, ErrQuarantineRefused) {
		t.Fatalf("activation of the doubled torn record was not refused: %v", err)
	}
	if exists(t, quarantinePath(s.path)) || !bytes.Equal(fileBytes(t, s.path), before) {
		t.Fatal("a refused activation published a manifest or changed the record")
	}
	if in, err := InspectQuarantine(repo, d5Torn); err != nil || in.State != QuarantineIneligible {
		t.Fatalf("inspected as %+v (%v), not ineligible", in, err)
	}
	failsClosed(t, repo, "no manifest", ErrRecordUnreadable)

	writeManifest(t, s, encoded(t, forged(t, s)))
	failsClosed(t, repo, "forged manifest", ErrQuarantineInvalid)
	if err := createTask(t, otherStore(t, repo, "S-new"), d5Claimed); err == nil || errors.Is(err, ErrTaskQuarantined) {
		t.Fatalf("the doubled identity was read as validly quarantined: %v", err)
	}
}

// D5-W4: quarantine preserves the original record in place, byte for byte,
// and the manifest states its identity, exact size and digest, the complete
// prefix, the claims, the tail, and who authorized the recovery and where
// from. The record is not repaired: its own Store still cannot read it.
func TestD5W4QuarantinePreservesBytesIdentityAndProvenance(t *testing.T) {
	repo := t.TempDir()
	torn := tornStore(t, repo, d5Torn, d5Claimed)
	before := fileBytes(t, torn.path)
	auth := authorized(t, repo, d5Torn)
	m := activate(t, repo, d5Torn)
	if !bytes.Equal(fileBytes(t, torn.path), before) {
		t.Fatal("the quarantined record's bytes changed")
	}
	switch {
	case m.Schema != QuarantineSchema || m.Version != QuarantineVersion || m.Status != QuarantineAsserted:
		t.Fatalf("the manifest's schema or status is %+v", m)
	case m.SessionID != d5Torn || m.Record != ".sensei-code/sessions/"+d5Torn+"/events.jsonl":
		t.Fatalf("the manifest names %s at %s", m.SessionID, m.Record)
	case m.Size != int64(len(before)) || m.Size != auth.ExpectedSize || m.SHA256 != auth.ExpectedSHA256:
		t.Fatalf("the manifest asserts %d bytes with %s", m.Size, m.SHA256)
	case m.PrefixEvents != 2 || m.PrefixEnd+m.Tail.Size != m.Size || m.Tail.Kind != string(event.Output) || m.Tail.TaskID != d5Claimed:
		t.Fatalf("the manifest classifies the record as %+v", m)
	case strings.Join(m.Claims, ",") != d5Claimed:
		t.Fatalf("the manifest claims %q", m.Claims)
	case m.AuthorizedBy != d5Owner || m.Provenance != auth.Provenance || m.CreatedAt == "":
		t.Fatalf("the manifest's authorization is %q, %q, %q", m.AuthorizedBy, m.Provenance, m.CreatedAt)
	}
	written := fileBytes(t, quarantinePath(torn.path))
	if canonical, err := encodeManifest(m); err != nil || !bytes.Equal(canonical, written) {
		t.Fatalf("the published manifest is not the one returned: %v", err)
	}
	if _, err := torn.ReadRecord(); err == nil {
		t.Fatal("quarantine repaired the record")
	}
	entries, err := os.ReadDir(filepathDir(torn.path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if n := e.Name(); n != "events.jsonl" && n != "quarantine.json" && n != "locks" {
			t.Fatalf("activation left %s beside the record", n)
		}
	}
}

// filepathDir is the directory of a record's path, its separator kept.
func filepathDir(path string) string { return strings.TrimSuffix(path, "events.jsonl") }

// forged is a manifest built, without activation, from the record's own
// classification: everything a forger could compute.
func forged(t *testing.T, s *Store) QuarantineManifest {
	t.Helper()
	sc := classifyRecord(fileBytes(t, s.path))
	return QuarantineManifest{Schema: QuarantineSchema, Version: QuarantineVersion, Status: QuarantineAsserted,
		SessionID: d5Torn, Record: quarantineRecord(d5Torn), Size: sc.size, SHA256: sc.sha256,
		PrefixEnd: sc.prefixEnd, PrefixEvents: len(sc.events), Claims: sc.claims, Tail: sc.tail,
		AuthorizedBy: d5Owner, Provenance: "forged", CreatedAt: "2026-10-08T00:00:00Z"}
}

func writeManifest(t *testing.T, s *Store, raw []byte) {
	t.Helper()
	if err := os.WriteFile(quarantinePath(s.path), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func encoded(t *testing.T, m QuarantineManifest) []byte {
	t.Helper()
	raw, err := encodeManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// D5-W5: no manifest masquerades as recovery. A synthetic manifest of other
// bytes, an under- or over-claiming one, one of another session, one not in
// its canonical encoding or carrying an unknown member, a stale one, an
// orphan, and one beside a record that reads -- even one computed exactly
// from that record -- each fail closed. The control, the same forged values
// beside the torn record it describes, is what activation would have
// written, and is accepted.
func TestD5W5NoManifestMasqueradesAsRecovery(t *testing.T) {
	cases := map[string]func(t *testing.T, repo string, s *Store){
		"synthetic digest": func(t *testing.T, _ string, s *Store) {
			m := forged(t, s)
			m.SHA256 = strings.Repeat("0", 64)
			writeManifest(t, s, encoded(t, m))
		},
		"wrong size": func(t *testing.T, _ string, s *Store) {
			m := forged(t, s)
			m.Size--
			writeManifest(t, s, encoded(t, m))
		},
		"under-claiming": func(t *testing.T, _ string, s *Store) {
			m := forged(t, s)
			m.Claims = []string{}
			writeManifest(t, s, encoded(t, m))
		},
		"over-claiming": func(t *testing.T, _ string, s *Store) {
			m := forged(t, s)
			m.Claims = append(m.Claims, "task-d5-extra")
			writeManifest(t, s, encoded(t, m))
		},
		"another tail": func(t *testing.T, _ string, s *Store) {
			m := forged(t, s)
			m.Tail.Kind = string(event.Status)
			writeManifest(t, s, encoded(t, m))
		},
		"another session": func(t *testing.T, _ string, s *Store) {
			m := forged(t, s)
			m.SessionID, m.Record = "S-other", quarantineRecord("S-other")
			writeManifest(t, s, encoded(t, m))
		},
		"unauthorized": func(t *testing.T, _ string, s *Store) {
			m := forged(t, s)
			m.AuthorizedBy = ""
			writeManifest(t, s, encoded(t, m))
		},
		"another status": func(t *testing.T, _ string, s *Store) {
			m := forged(t, s)
			m.Status = "released"
			writeManifest(t, s, encoded(t, m))
		},
		"noncanonical bytes": func(t *testing.T, _ string, s *Store) {
			writeManifest(t, s, mustMarshal(t, forged(t, s)))
		},
		"unknown member": func(t *testing.T, _ string, s *Store) {
			raw := encoded(t, forged(t, s))
			writeManifest(t, s, bytes.Replace(raw, []byte("{\n"), []byte("{\n  \"released\": true,\n"), 1))
		},
		"stale": func(t *testing.T, repo string, s *Store) {
			activate(t, repo, d5Torn)
			writeTail(t, s, "more")
		},
		"copied from an identical record": func(t *testing.T, repo string, s *Store) {
			activate(t, repo, d5Torn)
			copy := otherStore(t, repo, "S-copy")
			writeTail(t, copy, string(fileBytes(t, s.path)))
			writeManifest(t, copy, fileBytes(t, quarantinePath(s.path)))
		},
	}
	for name, plant := range cases {
		repo := t.TempDir()
		s := tornStore(t, repo, d5Torn, d5Claimed)
		plant(t, repo, s)
		failsClosed(t, repo, name, ErrQuarantineInvalid)
	}
	// An orphan, and a manifest beside a readable record computed exactly
	// from it, assert nothing either.
	repo := t.TempDir()
	tornStore(t, repo, d5Torn, d5Claimed)
	activate(t, repo, d5Torn)
	orphan := otherStore(t, repo, "S-orphan")
	writeManifest(t, orphan, fileBytes(t, quarantinePath(recordPath(repo, d5Torn))))
	failsClosed(t, repo, "orphan", ErrQuarantineInvalid)

	repo = t.TempDir()
	readable := d5Prefix(t, repo)
	writeRaw(t, readable, d5Output(d5Torn, d5Claimed))
	writeManifest(t, readable, encoded(t, forged(t, readable)))
	failsClosed(t, repo, "beside a readable record", ErrQuarantineInvalid)
	if _, err := ActivateQuarantine(t.Context(), repo, d5Torn, authorized(t, repo, d5Torn)); !errors.Is(err, ErrQuarantineRefused) {
		t.Fatalf("a readable record was quarantined: %v", err)
	}

	// Control: the forged values beside the torn record they describe are a
	// valid assertion, so every refusal above is the defect it names.
	repo = t.TempDir()
	s := tornStore(t, repo, d5Torn, d5Claimed)
	writeManifest(t, s, encoded(t, forged(t, s)))
	if err := createTask(t, otherStore(t, repo, "S-new"), d5Unrelated); err != nil {
		t.Fatalf("the control manifest was refused: %v", err)
	}
}

// D5-W11: a size or digest that is not exactly the record's refuses recovery
// and publishes nothing, including when the record changes after the owner
// inspected it.
func TestD5W11ADigestMismatchRefusesRecovery(t *testing.T) {
	repo := t.TempDir()
	torn := tornStore(t, repo, d5Torn, d5Claimed)
	good := authorized(t, repo, d5Torn)
	flipped := []byte(good.ExpectedSHA256)
	if flipped[0] == '0' {
		flipped[0] = '1'
	} else {
		flipped[0] = '0'
	}
	for name, c := range map[string]struct {
		auth QuarantineAuthorization
		want error
	}{
		"digest":    {QuarantineAuthorization{good.ExpectedSize, string(flipped), d5Owner, "p"}, ErrQuarantineMismatch},
		"size":      {QuarantineAuthorization{good.ExpectedSize + 1, good.ExpectedSHA256, d5Owner, "p"}, ErrQuarantineMismatch},
		"uppercase": {QuarantineAuthorization{good.ExpectedSize, strings.ToUpper(good.ExpectedSHA256), d5Owner, "p"}, ErrQuarantineRefused},
		"short":     {QuarantineAuthorization{good.ExpectedSize, good.ExpectedSHA256[:63], d5Owner, "p"}, ErrQuarantineRefused},
		"no size":   {QuarantineAuthorization{-1, good.ExpectedSHA256, d5Owner, "p"}, ErrQuarantineRefused},
		"no owner":  {QuarantineAuthorization{good.ExpectedSize, good.ExpectedSHA256, " ", "p"}, ErrQuarantineRefused},
		"no origin": {QuarantineAuthorization{good.ExpectedSize, good.ExpectedSHA256, d5Owner, ""}, ErrQuarantineRefused},
	} {
		if _, err := ActivateQuarantine(t.Context(), repo, d5Torn, c.auth); !errors.Is(err, c.want) {
			t.Fatalf("%s: activation was not refused (%v): %v", name, c.want, err)
		}
	}
	writeTail(t, torn, "x")
	if _, err := ActivateQuarantine(t.Context(), repo, d5Torn, good); !errors.Is(err, ErrQuarantineMismatch) {
		t.Fatalf("a record changed after inspection was quarantined: %v", err)
	}
	if exists(t, quarantinePath(torn.path)) {
		t.Fatal("a refused activation published a manifest")
	}
	failsClosed(t, repo, "mismatch", ErrRecordUnreadable)
}

// D5-W12 (operator surface): the explicit quarantine command inspects,
// activates only with the owner's exact authorization, and reports the
// quarantined task as visible and not resumable.
func TestD5W12TheOperatorCommandShowsTheQuarantine(t *testing.T) {
	repo := t.TempDir()
	tornStore(t, repo, d5Torn, d5Claimed)
	auth := authorized(t, repo, d5Torn)
	var out bytes.Buffer
	if err := QuarantineCommand(t.Context(), repo, []string{"status"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), d5Torn+": eligible") {
		t.Fatalf("status before activation:\n%s", out.String())
	}
	if err := QuarantineCommand(t.Context(), repo, []string{"activate", "--session", d5Torn}, &out); err == nil {
		t.Fatal("activation without the owner's authorization ran")
	}
	if exists(t, quarantinePath(recordPath(repo, d5Torn))) {
		t.Fatal("an unauthorized activation published a manifest")
	}
	args := []string{"activate", "--session", d5Torn, "--expected-size", itoa(auth.ExpectedSize),
		"--expected-sha256", auth.ExpectedSHA256, "--authorized-by", d5Owner, "--provenance", "RULING-224 witness"}
	if err := QuarantineCommand(t.Context(), repo, args, &out); err != nil {
		t.Fatalf("an authorized activation failed: %v", err)
	}
	out.Reset()
	if err := QuarantineCommand(t.Context(), repo, []string{"inspect", "--session", d5Torn}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{d5Torn + ": quarantined", d5Claimed, "authorized by " + d5Owner, "not resumable"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("inspection does not say %q:\n%s", want, out.String())
		}
	}
}

// itoa is n in decimal.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

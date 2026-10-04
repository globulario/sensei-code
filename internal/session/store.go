package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
)

type Store struct {
	mu   sync.Mutex
	path string
}

func New(repo, sessionID string) (*Store, error) {
	p := recordPath(repo, sessionID)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	return &Store{path: p}, nil
}

// maxSessionEvent is the largest single durable session event Load will read.
//
// It matches the limit this repository already uses for a governed event carrying a
// whole candidate diff (runreceipt/legacy.maxLine, provider/codex_appserver.go) so
// there is ONE size policy rather than three. Finite deliberately: crossing it is an
// error, never a truncation.
const maxSessionEvent = 16 << 20

// initialSessionEventBuffer is the starting allocation, not the ceiling; Scanner
// grows it as needed up to maxSessionEvent. Ordinary events are far smaller, so
// this is sized for them and not for the rare diff-carrying one.
const initialSessionEventBuffer = 1 << 20

func (s *Store) Append(e event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// THE REFUSAL BELONGS HERE, BEFORE ANY BYTES ENTER DURABLE HISTORY.
	//
	// Bounding only the reader moved the poison pill from 64 KiB to 16 MiB; it did not
	// remove it. An Append that succeeds while the matching Load refuses is the same
	// contradiction at a higher threshold: the writer still manufactures a record
	// nothing can open, and by then it is durable.
	//
	// So the event is encoded into memory and measured first. Over the limit, nothing
	// is written and the caller is told; the existing session stays exactly as it was,
	// readable, with no partial line appended. Under it, the write is one call, so a
	// record that exists is a record Load can read.
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(e); err != nil {
		return err
	}
	// Encode appends the newline delimiter. Scanner's ceiling applies to the TOKEN,
	// which excludes that byte, so the token is what must fit -- measured rather than
	// assumed, because an off-by-one here is precisely the boundary that would make
	// Append and Load disagree again.
	if token := buf.Len() - 1; token > maxSessionEvent {
		return fmt.Errorf("session event for task %q is %d bytes, over the %d-byte maximum a single "+
			"event may occupy; it is refused before it is written, because a durable record that "+
			"Load cannot read back is worse than a rejected append", e.TaskID, token, maxSessionEvent)
	}

	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(buf.Bytes())
	return err
}

func (s *Store) Load() ([]event.Event, error) {
	f, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []event.Event
	sc := bufio.NewScanner(f)
	// THE WRITER MUST NOT BE ABLE TO CREATE A RECORD THE READER CANNOT OPEN.
	//
	// Append encodes an event with json.NewEncoder and bounds nothing, while this
	// reader used bufio.Scanner's DEFAULT 64 KiB token ceiling. Any event Sensei-Code
	// legitimately produces above that -- a candidate.changed carrying a whole diff --
	// made the durable session record unreadable, and unreadable is not local to one
	// line: Load fails, so FindInterrupted has nothing to derive from, so `resume` and
	// `resume --list` both fail and the preserved-candidate lifecycle becomes
	// unreachable. Observed at 172_623 bytes on a 3_236-line candidate: the larger and
	// more valuable the work, the more certainly its own history locked it out.
	//
	// 16 MiB is not a new policy. It is the limit this repository already treats as
	// authoritative for one governed event -- see runreceipt/legacy.maxLine, whose
	// comment names the same case ("one governed event can carry a whole candidate
	// diff"), and provider/codex_appserver.go. The ceiling stays FINITE on purpose: a
	// corrupt or hostile record must not be able to exhaust memory, and a bound that
	// is never reached is still the thing that makes exceeding it an error rather than
	// a silent truncation.
	// maxSessionEvent+1, not maxSessionEvent. Scanner's ceiling must leave room beyond
	// the token for the delimiter it scans past, so passing the bound verbatim makes a
	// token of EXACTLY the maximum unreadable -- accepted by Append, refused here, the
	// original asymmetry surviving at one specific size. Found by a boundary witness
	// that computes the encoding overhead and lands on the exact byte; an earlier
	// version stepped in 64-byte increments and sailed straight past it.
	sc.Buffer(make([]byte, 0, initialSessionEventBuffer), maxSessionEvent+1)
	for sc.Scan() {
		var e event.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			// Named rather than passed through. "token too long" said nothing about
			// which file, which ceiling, or that the record was otherwise intact --
			// it cost a diagnostic cycle to locate.
			return nil, fmt.Errorf("session record %s holds an event larger than the %d-byte maximum a single "+
				"event may occupy; it is refused rather than truncated, because a partially read event is not "+
				"the event that was written: %w", s.path, maxSessionEvent, err)
		}
		return nil, err
	}
	return out, nil
}

// ID mints a session identifier that sorts chronologically as a string, so the
// most recent session can be found without reading any file.
func ID(t time.Time) string {
	return "session-" + t.UTC().Format("20060102T150405.000000000Z")
}

// readableSessions is every session record this repository holds, oldest first.
//
// ONE definition of what counts as a record, because two of them disagreed. The
// question "is this session readable" is asked by Latest and by FindActive, and
// each had its own answer; both treated EVERY stat error as "no record here", so
// a permission failure, an I/O error or a symlink loop SHRANK the world being
// searched instead of stopping the search. A history nobody could open was
// reported as a history that holds nothing.
//
// A record is skipped ONLY when it does not exist: a session directory created
// and never written to. That is absence, and absence is answerable. Every other
// failure is returned, because "I could not look" is not "there is nothing
// there".
//
// Session ids sort chronologically (see ID), so the result reads in the order
// things happened.
func readableSessions(repo string) ([]string, error) {
	dir := filepath.Join(repo, ".sensei-code", "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// No sessions directory is ABSENCE: this repository has begun no
			// task. An unreadable one is not.
			return nil, nil
		}
		return nil, fmt.Errorf("the session records of %s could not be listed, so what this repository holds is "+
			"unknown rather than absent: %w", repo, err)
	}
	var ids []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(recordPath(repo, entry.Name())); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("the event record of session %s in %s could not be examined, so the tasks it "+
				"holds are unknown rather than absent: %w", entry.Name(), repo, err)
		}
		ids = append(ids, entry.Name())
	}
	sort.Strings(ids)
	return ids, nil
}

// Latest returns the most recent recorded session for this repository, so a
// relaunch can continue the conversation instead of starting blank.
//
// The error is the third answer and it is not optional. "No session" and "the
// sessions could not be read" were both reported as false, so a caller that
// prechecked this and stopped on false stated absence from ignorance -- and the
// caller that did not stop started a fresh session inside storage it had already
// failed to list.
func Latest(repo string) (string, bool, error) {
	ids, err := readableSessions(repo)
	if err != nil {
		return "", false, err
	}
	if len(ids) == 0 {
		return "", false, nil
	}
	return ids[len(ids)-1], true, nil
}

// Interrupted is a task whose creation is recorded and whose ending is not: the
// process died, was killed, or ended an invocation without ending the task.
//
// Its fields say what the task owes, and they are what routes a continuation.
// They may all be empty: a task interrupted between answering its authority
// question and having a plan proposed owes its architect turn, and owes it
// silently -- no question stands, no plan exists, nothing is blocked.
type Interrupted struct {
	TaskID string
	Task   string
	Plan   string
	// PlanSource and PlanDigest say who authored the plan, read from the
	// PlanProposed payload the engine wrote. PlanRecord is that payload, byte
	// for byte, for the same reason AwaitingAuthority is: a supplied plan is
	// resumed under the exact bound it was given, and a round trip through a
	// decoded shape is where a bound quietly becomes a different one. An
	// event with no source field predates the field, and only the architect
	// produced plans then.
	PlanSource string
	PlanDigest string
	PlanRecord json.RawMessage
	// PlanEventSource is who emitted the PlanProposed event: the architect for
	// its own plan, the system for a supplied one. It is the independent fact
	// that lets a record with no plan_source field be told apart from a
	// supplied record that lost the field; an absent field alone proves
	// nothing about which it is.
	PlanEventSource event.Source
	// Review is the last thing the reviewer said, which is the most useful
	// thing to hand whoever picks the work up.
	Review string
	// AwaitingAuthority is the Level-3 question this task left standing, when
	// it left one. It is carried as the raw recorded payload rather than a
	// decoded value on purpose: resuming must ask the question that was asked,
	// byte for byte, and a round trip through this package's own idea of the
	// shape is exactly where a question quietly becomes a different question.
	//
	// A task holding one is not resumed by continuing the work. It is resumed
	// by asking it again, and only an explicit answer moves past it.
	AwaitingAuthority json.RawMessage
	// ProspectiveRecord is the prospective authorization the router recorded
	// for this task's declared new surfaces, byte for byte, so a resumed task
	// inspects a created file against the facts that authorized it. Absent
	// when the task declared none, or when the record was never written; the
	// engine tells those apart from the plan and refuses the latter.
	ProspectiveRecord json.RawMessage
	// TestEditRecord is the existing-test edit authorization the router
	// recorded (M2.2), carried byte for byte for the same reason.
	TestEditRecord json.RawMessage
	// PlanAttemptID is the canonical identity of the OPERATIVE plan attempt:
	// the plan_attempt_id the newest PlanProposed carries. Empty when no plan
	// is operative, or when the operative plan was recorded before plan
	// attempts existed; no identity is ever minted for such a record.
	//
	// It decides which grant records the task holds. Once a plan attempt is
	// operative, ProspectiveRecord and TestEditRecord are the records bound to
	// THAT id and to no other: a record from a superseded or refused attempt
	// never returns because it was written later or names the same paths, and
	// an operative attempt with no record of its own holds none -- absence is
	// not an empty grant set, and is left for the restorer to refuse.
	PlanAttemptID string
	// PlanAttemptRefusals are the plan-admission refusals this task recorded,
	// keyed by each one's canonical RefusalID, payload byte for byte; each
	// payload names the PlanAttemptID it refused. Keyed by refusal and not by
	// attempt because one attempt may be refused more than once under
	// different governing evidence, and a later refusal must not erase an
	// earlier one. A record written before refusals carried a RefusalID is
	// keyed by the PlanAttemptID it refused, as it always was. Evidence about
	// those exact attempts: a refusal never attaches to another attempt and
	// never makes the task planned. Only a refusal recorded after its
	// attempt's PlanAttemptStarted is kept here.
	PlanAttemptRefusals map[string]json.RawMessage
	// PlanAdmissionRefused is the plan-admission refusal the task is currently
	// OWED an architect turn for, payload byte for byte, named by its
	// PlanAttemptID and RefusalID: the newest canonical PlanAttemptRefused
	// whose recorded continuation is PlanAdmissionContinuationArchitectTurn (a
	// first occurrence the workflow returned to the architect), or
	// WorkflowPlanAdmissionRefused (a repeat an invocation parked on). It is
	// the durable statement of which refusal is owed, read in record order and
	// not from the RefusalID-keyed map. A later PlanProposed -- an admitted
	// plan -- discharges it, including when an older plan was already
	// operative. Which refusals return to the architect is the workflow
	// package's to decide, and it records that decision on the refusal; this
	// projection reads the recorded decision and never infers it.
	PlanAdmissionRefused json.RawMessage
	// PlanAttemptStart is the FIRST PlanAttemptStarted record of the operative
	// attempt, payload byte for byte: the durable prerequisite every authority
	// record and the operative PlanProposed of that attempt stand on. Absent
	// when no attempt-bearing plan is operative, or when that attempt's start
	// was never recorded -- which the restorer refuses, never mints.
	PlanAttemptStart json.RawMessage
	// PlanAttemptStartFirst reports that PlanAttemptStart was recorded BEFORE
	// the operative PlanProposed and before both grant records selected for
	// that attempt. A start written after the authority it is meant to precede
	// does not establish it.
	PlanAttemptStartFirst bool
	// StartedPlanAttempts are the PlanAttemptIDs this task durably started, so
	// a record naming an attempt (a deferred question, say) can be checked
	// against an attempt that was actually recorded rather than trusted.
	StartedPlanAttempts map[string]bool
	// AwaitingReview marks a task whose candidate stands and whose required
	// independent review has not happened.
	//
	// It is carried because resuming such a task is not resuming ordinary
	// work. The candidate was accepted by a reviewer; what is missing is a
	// reviewer whose independence could be established. Handing it to an
	// implementer would ask a worker to change code nobody objected to, purely
	// because a process restarted.
	AwaitingReview bool
	// AwaitingReviewRecord is the WAITING_REVIEW terminal's payload, byte for
	// byte: which review is owed, under which request, about which exact
	// candidate. A continuation compares the candidate it resumes against this
	// rather than against anything re-derived after the restart. Cleared with
	// AwaitingReview, so a revised task never carries an obligation it no longer
	// owes.
	AwaitingReviewRecord json.RawMessage
	// Planned reports whether the task ever received a bounded plan. A resume
	// routes on it: a task with no plan is owed its architect turn, one with a
	// plan is owed implementation.
	Planned bool
	// BlockedExternal is the WorkflowBlockedExternal payload, byte for byte:
	// which role turn the task is owed and which provider proved it could not
	// serve it. The newest block wins. An ARCHITECT block is discharged by a
	// later PlanProposed -- the architect turn it was owed was delivered -- so a
	// planned task is never sent back to re-plan by a turn it already took.
	BlockedExternal json.RawMessage
	// NotConverged is the WorkflowNotConverged payload, byte for byte: every
	// implementer spent its review cycles and the task is owed an architect
	// re-plan. A later PlanProposed -- the re-plan a resume records -- discharges
	// it.
	NotConverged json.RawMessage
	// RestorationRefused is the WorkflowRestorationRefused payload, byte for
	// byte: which recorded authority a resume could not re-establish, and which
	// instrument binding it could not read or verify.
	//
	// It is carried as EVIDENCE about the task, not as an obligation that
	// routes: the task owes exactly what it owed before the refusal, and the
	// refusal says why the last attempt did not execute. The newest one wins.
	// Carried at all because a refusal whose reason is unreadable after a
	// restart is indistinguishable from a task that was never attempted.
	RestorationRefused json.RawMessage
	// PreconditionRefusal is the kind of the newest candidate-precondition
	// refusal -- WorkflowBaseMovedRefused or WorkflowDirtyCanonicalRefused --
	// and PreconditionRefusalReason is what it said. Evidence, like
	// RestorationRefused: the task owes what it owed before, a standing
	// question included, and this says why the last attempt did not execute.
	PreconditionRefusal       event.Kind
	PreconditionRefusalReason string
}

// blockedRole reads only the role an external-block record names. The workflow
// package owns the record's full shape; this package needs one field of it and
// must not import that package.
func blockedRole(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var b struct {
		Role string `json:"role"`
	}
	if json.Unmarshal(raw, &b) != nil {
		return ""
	}
	return b.Role
}

// planAttemptOf reads only the plan_attempt_id a record names: "" for a record
// written before plan attempts existed. The workflow package owns the records'
// full shapes; this package needs one field of them and must not import it.
func planAttemptOf(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var r struct {
		PlanAttemptID string `json:"plan_attempt_id"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return ""
	}
	return r.PlanAttemptID
}

// PlanAdmissionContinuationArchitectTurn is the continuation the workflow
// records on a PlanAttemptRefused when it returned that refusal to the
// architect, so the task is owed an architect turn for it. Read by membership:
// a refusal recorded with any other continuation, or none, owes nothing.
const PlanAdmissionContinuationArchitectTurn = "architect_turn"

// refusalOf reads only the refusal_id a refusal record names ("" for a record
// written before refusals carried one) and the continuation the workflow
// recorded on it. The workflow package owns the record's full shape.
func refusalOf(raw json.RawMessage) (id, continuation string) {
	if len(raw) == 0 {
		return "", ""
	}
	var r struct {
		RefusalID    string `json:"refusal_id"`
		Continuation string `json:"continuation"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return "", ""
	}
	return r.RefusalID, r.Continuation
}

// FindInterrupted reconstructs, from one session record, every task that has
// begun and not ended, together with what each of them currently owes.
//
// From the session record rather than from a second bookkeeping file: the log is
// already the account of what happened, a parallel state file could disagree
// with it, and then neither could be trusted.
//
// It is a RECONSTRUCTION, not a latch. The log is append-only and a process
// reuses it, so "this task once awaited a review" and "this task once deferred a
// question" stay true for ever; what a continuation needs is what the task owes
// NOW, which is why several cases below clear what an earlier event set.
//
// The lifetime is bounded by two events and nothing else: task.created opens it,
// a task-terminal event closes it. Deriving existence from the obligations
// instead -- resumable if planned, or deferring, or blocked -- left the task
// undiscoverable in every window between them.
func FindInterrupted(events []event.Event) []Interrupted {
	type partial struct {
		Interrupted
		created bool
		planned bool
		done    bool
		// reviewFromVerdict records that Review holds a bounded verdict's
		// instruction, so a later status line cannot replace an obligation
		// with a sentence about it.
		reviewFromVerdict bool
		// prospective and testEdits are the newest grant record of each kind
		// per plan attempt ("" for records that predate plan attempts). Which
		// one the task holds is decided once, by the operative attempt.
		prospective map[string]json.RawMessage
		testEdits   map[string]json.RawMessage
		// The position in the record of each attempt's first start, of the
		// operative PlanProposed and of each attempt's selected grant records,
		// so the start can be shown to precede the authority it establishes.
		startAt       map[string]int
		starts        map[string]json.RawMessage
		proposedAt    int
		prospectiveAt map[string]int
		testEditsAt   map[string]int
	}
	order := []string{}
	byTask := map[string]*partial{}
	get := func(id string) *partial {
		if id == "" {
			return nil
		}
		if _, ok := byTask[id]; !ok {
			byTask[id] = &partial{Interrupted: Interrupted{TaskID: id},
				prospective: map[string]json.RawMessage{}, testEdits: map[string]json.RawMessage{},
				startAt: map[string]int{}, starts: map[string]json.RawMessage{},
				prospectiveAt: map[string]int{}, testEditsAt: map[string]int{}}
			order = append(order, id)
		}
		return byTask[id]
	}
	for at, e := range events {
		p := get(e.TaskID)
		if p == nil {
			continue
		}
		switch e.Kind {
		case event.TaskCreated:
			// THE CONTINUITY ROOT. A task exists from the moment its creation is
			// recorded, and it keeps existing until something records that it
			// ended. Everything else in this switch describes what it OWES, not
			// whether it is still there.
			//
			// This used to be one of several ways in: a task became visible when
			// it was planned, deferred a question or was blocked, and was
			// invisible otherwise. Every gap between those states was a hole a
			// task could fall into. task-1789848074761930104 (2026-09-19) fell
			// into the one between authority.resolved and plan.proposed: the
			// owner answered the standing question, the run re-entered execution,
			// the architect began re-planning and the process died before any
			// plan existed. The question was cleared, no plan was recorded and
			// nothing was blocked, so the task was in none of the three states --
			// `resume --list` omitted it and `resume --task` answered "no
			// interrupted task with that id" about a task whose candidate
			// identity was still on disk.
			p.created = true
			p.Task = e.Summary
		case event.PlanProposed:
			// A bounded plan is what makes a task resumable: /resume re-enters
			// implementation with it, and a task that never got one has nothing
			// to continue.
			//
			// This used to key off AuthorityResolved, from the approval prompt
			// that stood between the plan and the worker. Removing that prompt
			// removed the event, and with it every governed task's claim to be
			// resumable — a stopped run would have been unrecoverable, which is
			// exactly what a stop must not be. The signal is the plan, not the
			// human's yes to it.
			p.Plan = e.Summary
			p.planned = true
			p.PlanRecord = e.Payload
			// A plan discharges an architect turn a block was holding, and the
			// re-plan a non-converged task was owed: a resume records its re-plan
			// as exactly this event, so what it owes next is the implementation
			// of THAT plan.
			if blockedRole(p.BlockedExternal) == "architect" {
				p.BlockedExternal = nil
			}
			p.NotConverged = nil
			// An admitted plan is the architect turn an owed plan-admission
			// refusal was holding.
			p.PlanAdmissionRefused = nil
			p.PlanEventSource = e.Source
			var src struct {
				Source string `json:"plan_source"`
				Digest string `json:"plan_digest"`
			}
			if len(e.Payload) != 0 && json.Unmarshal(e.Payload, &src) == nil {
				p.PlanSource, p.PlanDigest = src.Source, src.Digest
			}
			// The newest PlanProposed is the operative attempt, and it
			// supersedes the previous one whole: a legacy plan recorded after
			// an attempt-bearing one has no attempt, not the previous one's.
			p.PlanAttemptID = planAttemptOf(e.Payload)
			p.proposedAt = at
		case event.PlanAttemptStarted:
			// The FIRST start of an attempt is its prerequisite; a later start
			// of the same identity (the same plan routed again) adds nothing.
			if id := planAttemptOf(e.Payload); id != "" {
				if _, seen := p.starts[id]; !seen {
					p.starts[id], p.startAt[id] = e.Payload, at
				}
			}
		case event.ProspectiveGranted:
			p.prospective[planAttemptOf(e.Payload)] = e.Payload
			p.prospectiveAt[planAttemptOf(e.Payload)] = at
		case event.TestEditGranted:
			p.testEdits[planAttemptOf(e.Payload)] = e.Payload
			p.testEditsAt[planAttemptOf(e.Payload)] = at
		case event.PlanAttemptRefused:
			if id := planAttemptOf(e.Payload); id != "" {
				key, continuation := refusalOf(e.Payload)
				if key != "" && continuation == PlanAdmissionContinuationArchitectTurn {
					// A canonical refusal the workflow returned to the
					// architect is owed an architect turn from the moment it
					// is recorded -- the first occurrence, not only a parked
					// repeat -- until an admitted plan discharges it, whether
					// or not an older plan is operative. Any other refusal is
					// evidence only. Owed even when the refusal below is not
					// admitted: the obligation is preserved so restoration
					// refuses its binding by name instead of losing it.
					p.PlanAdmissionRefused = e.Payload
				}
				// A refusal is bound to an attempt only when that attempt was
				// durably started EARLIER in the record. A refusal recorded
				// before its start refused nothing this task had routed, and a
				// later start of the same identity does not validate it
				// retroactively, so it never enters the refusals restoration
				// reads as repetition state. This fold is the one place that
				// order is still visible.
				if _, started := p.starts[id]; !started {
					break
				}
				if key == "" {
					key = id
				}
				if p.PlanAttemptRefusals == nil {
					p.PlanAttemptRefusals = map[string]json.RawMessage{}
				}
				p.PlanAttemptRefusals[key] = e.Payload
			}
		case event.WorkflowPlanAdmissionRefused:
			// Not terminal: the invocation parked on a repeated refusal of a
			// plan, and the task is owed an architect turn under it.
			p.PlanAdmissionRefused = e.Payload
		case event.WorkflowStopped:
			// Deliberately not terminal. A stop is the human withdrawing
			// attention, and the whole point of leaving the candidate as it
			// stands is that it can be picked back up.
		case event.WorkflowAwaitingReview:
			// Not terminal. The invocation is over and the task is not: the
			// candidate stands, and the independent review it owes can still
			// be obtained. This case exists because emitting the condition as
			// WorkflowFailed made it invisible here, and a task nobody could
			// find again is not "preserved awaiting review" however carefully
			// the receipt says so.
			p.AwaitingReview = true
			// The terminal's own record of what is owed, carried byte for byte.
			// A later terminal replaces it: the newest statement of the
			// obligation is the one a continuation must honour.
			p.AwaitingReviewRecord = e.Payload
		case event.ReviewCompleted:
			// A bounded verdict may END the awaiting-review state, and this is
			// where the difference between a latch and a reconstruction lives.
			//
			// The session log is append-only and the interactive process reuses
			// it, so "this task once awaited a review" stays true forever. What
			// a resume needs is what the task owes NOW. A REVISE or an ESCALATE
			// says the candidate is no longer in "nothing is wrong, only an
			// independent look is missing" -- it owes a change, or an
			// architectural answer -- and resuming into a review would ask the
			// same question again while the finding nobody acted on aged.
			//
			// ACCEPT deliberately does NOT clear it. An advisory accept is
			// followed by a fresh WorkflowAwaitingReview, and a crash between
			// those two events must not turn a candidate nobody objected to
			// into implementation work. Neither does ReviewStarted: a process
			// that died mid-review still owes the review.
			var verdict roles.ReviewVerdict
			if len(e.Payload) != 0 && json.Unmarshal(e.Payload, &verdict) == nil {
				switch roles.Decision(strings.ToLower(strings.TrimSpace(string(verdict.Decision)))) {
				case roles.Revise, roles.Escalate:
					p.AwaitingReview = false
					p.AwaitingReviewRecord = nil
				}
				// The latest bounded verdict is what the next actor is handed,
				// rendered by the SAME method that tells a live worker what to
				// fix.
				//
				// Instruction() and not Summary. The summary is presentation --
				// "proof incomplete" -- and the obligation is the findings: what
				// was claimed, the file to open, the correction or the missing
				// proof, and any explicit instructions. A restart that handed
				// the worker the summary would silently weaken the brief from
				// "here is the file and the fix" to a sentence, and the loss
				// would be invisible because both are non-empty strings.
				//
				// Rendering here rather than re-implementing it: a second
				// renderer in this package would drift from the one the live
				// path uses, and the resumed worker would be told something
				// slightly different from what the interrupted one was told.
				// Instruction() already falls back to the summary when a
				// verdict carries nothing more specific.
				if instruction := strings.TrimSpace(verdict.Instruction()); instruction != "" {
					p.Review = instruction
					p.reviewFromVerdict = true
				}
			}
		case event.WorkflowBlockedExternal:
			// Not terminal, and resumable even with no plan: a provider that
			// proved it cannot serve now blocked a turn the task is still owed.
			// Emitting this as WorkflowFailed is exactly what made the first
			// dogfood run (2026-09-18) unrecoverable except as a new task.
			p.BlockedExternal = e.Payload
		case event.WorkflowRestorationRefused:
			// NOT TERMINAL, and this is the whole repair: a resume refused to
			// execute under authority it could not verify, and the task it
			// refused must still be here afterwards. Emitted as WorkflowFailed
			// it was final here, so the safety check permanently destroyed the
			// obligation it was protecting (task-1789960053774525922,
			// 2026-09-21).
			p.RestorationRefused = e.Payload
		case event.WorkflowBaseMovedRefused, event.WorkflowDirtyCanonicalRefused:
			// Not terminal, and deliberately NOT a clearing of the standing
			// question: the refusal happened before any answer was consumed,
			// so the question stands exactly as it was asked.
			p.PreconditionRefusal = e.Kind
			p.PreconditionRefusalReason = e.Summary
		case event.WorkflowNotConverged:
			// Not terminal: the candidate stands and the task is owed an
			// architect re-plan. Emitted as WorkflowFailed it was final here while
			// the candidate record called the same work resumable (DF-6).
			p.NotConverged = e.Payload
		case event.WorkflowAwaitingAuthority:
			// Also not terminal, and resumable even with no plan: a question
			// deferred during architecture is the ordinary case, and it is
			// reached before any plan exists.
			p.AwaitingAuthority = e.Payload
		case event.AuthorityResolved:
			// The standing question was answered, so there is nothing left to
			// restore. Clearing it matters: a task deferred, resumed, answered
			// and interrupted again must resume as work, not as the question
			// it already settled.
			p.AwaitingAuthority = nil

		}
		// THE TASK-TERMINAL PREDICATE is the event package's, and only it: the
		// task-terminal set lives in one place (event.RunTerminality) so a
		// reader here cannot drift from how the endings were classified. Every
		// INVOCATION terminal ends one process's attempt and leaves the task
		// owing something, which is precisely the state this function exists
		// to report.
		if terminality, ok := event.RunTerminality(e.Kind); ok && terminality == event.TaskTerminal {
			p.done = true
		}
		// A reviewer's status line is the fallback, for records written before
		// ReviewCompleted carried a payload. It must not overwrite a bounded
		// verdict's instruction: a status line is presentation, the instruction
		// is the obligation, and the reviewer emits several status lines around
		// an advisory accept that would otherwise be the last thing written.
		if !p.reviewFromVerdict &&
			e.Source == event.SourceReviewer && e.Kind == event.Status && strings.TrimSpace(e.Summary) != "" {
			p.Review = e.Summary
		}
	}
	var out []Interrupted
	for _, id := range order {
		p := byTask[id]
		// CREATED AND NOT ENDED. Those two facts, and nothing else.
		//
		// A nonblank objective used to be a third condition here, on the
		// reasoning that a task which cannot say what it is for cannot be
		// resumed as anything. That reasoning is sound and the conclusion was
		// wrong: task.created is written BEFORE execute validates the objective,
		// so a process that dies in that interval leaves a created, nonterminal
		// task with a blank one -- and filtering it out made that task VANISH,
		// which is the very absence-for-unknown class this reconstruction
		// exists to remove. A task nobody can find is worse than a task that
		// says plainly why it cannot be continued.
		//
		// So it stays discoverable and ObjectiveUsable says what is wrong with
		// it; the router refuses the continuation by name. Discoverability and
		// eligibility are two statements, and both of them get made.
		if p.created && !p.done {
			p.Interrupted.Planned = p.planned
			// The grant records the operative attempt owns, and only those.
			// With no attempt-bearing plan operative, only records that
			// predate plan attempts are read, exactly as before them.
			p.Interrupted.ProspectiveRecord = p.prospective[p.PlanAttemptID]
			p.Interrupted.TestEditRecord = p.testEdits[p.PlanAttemptID]
			if id := p.PlanAttemptID; id != "" {
				if start, ok := p.starts[id]; ok {
					first := p.startAt[id] < p.proposedAt
					if g, ok := p.prospectiveAt[id]; ok && g < p.startAt[id] {
						first = false
					}
					if g, ok := p.testEditsAt[id]; ok && g < p.startAt[id] {
						first = false
					}
					p.Interrupted.PlanAttemptStart, p.Interrupted.PlanAttemptStartFirst = start, first
				}
			}
			if len(p.starts) != 0 {
				p.Interrupted.StartedPlanAttempts = map[string]bool{}
				for id := range p.starts {
					p.Interrupted.StartedPlanAttempts[id] = true
				}
			}
			out = append(out, p.Interrupted)
		}
	}
	return out
}

// ObjectiveUsable reports whether this task recorded what it is for.
//
// A created, nonterminal task with a blank or whitespace-only objective is
// REAL -- its identity is recorded, its candidate may be on disk, and it has not
// ended -- but it cannot be continued as anything: the governed path refuses an
// empty objective, and no continuation can invent one without inventing the work
// the owner asked for. The honest answer is to show it and refuse it, not to
// hide it.
func (i Interrupted) ObjectiveUsable() bool {
	return strings.TrimSpace(i.Task) != ""
}

// Active is one task that has begun and not ended, together with the session
// record that holds it.
//
// The session travels with the task because a continuation must append to THAT
// record. Resuming into a different session would leave the question in one
// file and its answer in another, and the next reconstruction would find a task
// that is still asking beside one that has already been told.
type Active struct {
	SessionID string
	Task      Interrupted
}

// Discovery is the outcome of searching a repository for active tasks: the
// tasks themselves, and which session records were searched to find them.
//
// Records is carried because zero active tasks has two causes and a caller has
// to tell them apart. A repository that has never run anything holds no records;
// one that has run plenty holds records in which nothing is still active. Only
// the first is "there is no session here", and a caller that could not tell
// printed that sentence about both.
//
// It names the records rather than counting them, because a caller that wants
// to speak about ONE of them has to be able to tell "that record holds nothing
// active" from "there is no such record", and a count answers neither. That is
// what makes ScopedTo a filter over a validated world instead of a second,
// narrower read of storage.
type Discovery struct {
	// Records is every session record that was read, oldest first. Empty means
	// this repository has begun nothing -- never that reading failed, because a
	// failure is returned as an error and this struct is not.
	Records []string
	Active  []Active
}

// ScopedTo narrows an already-validated inventory to the tasks of ONE session
// record.
//
// NAMING A SESSION IS A FILTER, NEVER A WAIVER, and this being a method on
// Discovery is how that is enforced: there is no way to reach it without having
// read every record first. The per-record reader this replaces was the path
// `--session` took, and it answered about the named record ALONE. So it could
// not see that another record -- still active, or already ended -- claimed the
// same task id, and it never met a history elsewhere that nobody could open. A
// person could name one side of a split identity and continue it, and be told
// nothing. A question about a subset is not a waiver of what makes the whole set
// trustworthy.
//
// A session that holds no record is an error rather than an empty answer, for
// the reason FindActive refuses an unreadable one: "nothing is active there" and
// "there is no such account" are different statements, and a typo makes only the
// second one true.
func (d Discovery) ScopedTo(sessionID string) ([]Active, error) {
	sessionID = strings.TrimSpace(sessionID)
	known := false
	for _, id := range d.Records {
		if id == sessionID {
			known = true
			break
		}
	}
	if !known {
		return nil, fmt.Errorf("session %s is not among the %d session records this repository holds, so nothing "+
			"can be said about what it left active", sessionID, len(d.Records))
	}
	var out []Active
	for _, entry := range d.Active {
		if entry.SessionID == sessionID {
			out = append(out, entry)
		}
	}
	return out, nil
}

// FindActive is every active task in this repository, across every session
// record it holds.
//
// Across, and not "in the most recent": a session is one process's account, and
// a task outlives the process that began it. Reading only the newest record made
// a task unfindable the moment anything else ran -- the same disappearance
// `--session` was added to work around, one layer further out.
//
// IT FAILS CLOSED, twice.
//
// A session record that cannot be read is an error, never an empty result. The
// task being looked for may be inside it, and reporting "no such task" from a
// history nobody could open states absence where the truth is ignorance. That is
// how a resumable task becomes an unrecoverable one: not by being lost, but by
// being confidently declared gone. readableSessions makes that policy one
// definition rather than a habit repeated at each call site.
//
// Two records claiming the same task id are refused rather than merged or
// ordered. There is no honest way to pick which account of that task is the one
// to continue, and continuing the wrong one would append this run's events to a
// history that describes different work.
//
// THE REFUSAL IS KEYED ON CREATION, NOT ON ACTIVITY, and that is the whole
// correction. It used to compare the tasks ActiveIn returned, which have already
// been filtered by lifecycle state: one record holding an active task.created
// and another holding the SAME identity followed by completed passed the check,
// because only one of the two accounts was ever offered for comparison. The
// identity was split and nothing said so, and a continuation could be appended
// to an account describing different work. createdIdentities reads the creations
// themselves -- terminal or not -- so a split identity is caught by the fact that
// makes it one, which is that two records both claim to have begun it.
//
// WHY EACH RECORD IS RECONSTRUCTED SEPARATELY. A task's events live in exactly
// one record: task.created is written once, and a continuation appends to the
// record that holds it. Measured on this repository's own history before this
// change -- 250 session records, 240 tasks, zero tasks with events in more than
// one record -- so obligations reconstructed per record are the whole account of
// the task. The duplicate refusal above is what catches a world where that
// stopped being true, rather than quietly reading half a history as all of it.
func FindActive(repo string) (Discovery, error) {
	ids, err := readableSessions(repo)
	if err != nil {
		return Discovery{}, err
	}
	found := Discovery{Records: ids}
	// Where each task identity was CREATED, which is the only claim to owning
	// that task's history. It is filled from every record, whatever lifecycle
	// state the task reached there.
	createdIn := map[string]string{}
	for _, id := range ids {
		history, err := loadRecord(repo, id)
		if err != nil {
			return Discovery{}, err
		}
		created, err := createdIdentities(id, history)
		if err != nil {
			return Discovery{}, err
		}
		for _, taskID := range created {
			if other, ok := createdIn[taskID]; ok {
				return Discovery{}, fmt.Errorf("task %s is recorded as created in two session records, %s and %s; "+
					"which of them is the account to continue cannot be decided from the records themselves, so "+
					"neither is chosen", taskID, other, id)
			}
			createdIn[taskID] = id
		}
		for _, task := range FindInterrupted(history) {
			found.Active = append(found.Active, Active{SessionID: id, Task: task})
		}
	}
	return found, nil
}

// createdIdentities is every task identity whose CREATION one record states, in
// the order the creations appear.
//
// Terminal tasks included, deliberately: this answers "which tasks does this
// record claim to have begun", and a task that has since ended was still begun
// here. That is what makes it usable as the identity inventory FindActive
// compares across records.
//
// A record that creates one identity twice is refused rather than reconciled.
// task.created is written once per task, so a second one means either two
// different pieces of work were given one name or a record was concatenated from
// two histories; FindInterrupted would fold both into a single reconstruction
// and report one task whose obligations are a mixture of the two, and there is
// no rule that recovers which events belong to which.
func createdIdentities(sessionID string, history []event.Event) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, e := range history {
		if e.Kind != event.TaskCreated {
			continue
		}
		// A creation with no task id names nothing, and FindInterrupted
		// already ignores it; it is not an identity anyone can claim twice.
		id := strings.TrimSpace(e.TaskID)
		if id == "" {
			continue
		}
		if seen[id] {
			return nil, fmt.Errorf("session record %s states the creation of task %s more than once, so the events "+
				"it holds cannot be attributed to one task; no account of that task is offered", sessionID, id)
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// loadRecord reads one session record, naming what the failure means.
func loadRecord(repo, sessionID string) ([]event.Event, error) {
	// The Store is built directly rather than through New, which creates
	// directories. A read must not bring into existence the thing it reports on.
	history, err := (&Store{path: recordPath(repo, sessionID)}).Load()
	if err != nil {
		return nil, fmt.Errorf("the session record of %s could not be read, so the tasks it left active are "+
			"unknown rather than absent: %w", sessionID, err)
	}
	return history, nil
}

// THERE IS DELIBERATELY NO PER-RECORD DISCOVERY FUNCTION HERE.
//
// One used to exist -- ActiveIn(repo, sessionID) -- and `--session` took it,
// which made naming a session a way to opt out of everything FindActive
// establishes about the repository as a whole. It is gone rather than merely
// unused, because an authority-bearing shortcut that still compiles is one a
// later caller will reach for. Ask FindActive, then Discovery.ScopedTo.

func recordPath(repo, sessionID string) string {
	return filepath.Join(repo, ".sensei-code", "sessions", sessionID, "events.jsonl")
}

// THE DURABLE INCOMPLETE-OBLIGATION CHECKPOINT (70B1, RULING-153).
//
// This package owns a checkpoint's FRAMING and its TRANSACTION RECORDS, and
// nothing about what it means. A checkpoint's inputs are opaque here: they are
// the canonical inputs the workflow's landed owners consume, and only replaying
// them through those owners says whether they describe an obligation at all.
// What is decided here is closed and mechanical: exactly one canonical value,
// a status and retirement reason read by membership, and which prepared and
// committed records prove they belong together.

// CheckpointStatus is the closed durable status of a checkpoint. Exactly four
// values exist. serving is transient and is never durable; closed is not a
// durable state. Read by membership: anything else is refused.
type CheckpointStatus string

const (
	CheckpointLive      CheckpointStatus = "live"
	CheckpointBlocked   CheckpointStatus = "blocked"
	CheckpointExhausted CheckpointStatus = "exhausted"
	CheckpointRetired   CheckpointStatus = "retired"
)

// Valid reports membership in the closed status vocabulary.
func (s CheckpointStatus) Valid() bool {
	switch s {
	case CheckpointLive, CheckpointBlocked, CheckpointExhausted, CheckpointRetired:
		return true
	}
	return false
}

// RetirementReason is the closed reason a retired checkpoint states. It is
// never prose.
type RetirementReason string

const (
	RetiredCompleted             RetirementReason = "completed"
	RetiredSupersededPlanAttempt RetirementReason = "superseded_plan_attempt"
	RetiredReplacedCheckpoint    RetirementReason = "replaced_checkpoint"
)

// Valid reports membership in the closed retirement vocabulary.
func (r RetirementReason) Valid() bool {
	switch r {
	case RetiredCompleted, RetiredSupersededPlanAttempt, RetiredReplacedCheckpoint:
		return true
	}
	return false
}

// CheckpointVersion is the framing version this package reads and writes.
const CheckpointVersion = 1

// Checkpoint is one durable checkpoint payload: its identity, its ownership,
// its typed status, and the opaque canonical inputs its owners replay.
type Checkpoint struct {
	Version              int              `json:"version"`
	CheckpointID         string           `json:"checkpoint_id"`
	PreviousCheckpointID string           `json:"previous_checkpoint_id,omitempty"`
	TaskID               string           `json:"task_id"`
	PlanAttemptID        string           `json:"plan_attempt_id"`
	Status               CheckpointStatus `json:"status"`
	// Retirement is set exactly when Status is retired.
	Retirement RetirementReason `json:"retirement,omitempty"`
	// Inputs are the canonical replay inputs, owned by the workflow package.
	Inputs json.RawMessage `json:"inputs"`
}

// checkFraming refuses a checkpoint whose framing is not closed and typed.
func (c Checkpoint) checkFraming() error {
	switch {
	case c.Version != CheckpointVersion:
		return fmt.Errorf("checkpoint framing version %d is not %d", c.Version, CheckpointVersion)
	case !validCheckpointID(c.CheckpointID):
		return fmt.Errorf("checkpoint id %q is not a checkpoint identity", c.CheckpointID)
	case c.PreviousCheckpointID != "" && !validCheckpointID(c.PreviousCheckpointID):
		return fmt.Errorf("previous checkpoint id %q is not a checkpoint identity", c.PreviousCheckpointID)
	case strings.TrimSpace(c.TaskID) == "" || strings.TrimSpace(c.PlanAttemptID) == "":
		return errors.New("a checkpoint names no task or no plan attempt")
	case !c.Status.Valid():
		return fmt.Errorf("checkpoint status %q is not a durable status", c.Status)
	case c.Status == CheckpointRetired && !c.Retirement.Valid():
		return fmt.Errorf("a retired checkpoint states retirement reason %q, which is not a retirement reason", c.Retirement)
	case c.Status != CheckpointRetired && c.Retirement != "":
		return fmt.Errorf("a %s checkpoint states a retirement reason", c.Status)
	case len(c.Inputs) == 0:
		return errors.New("a checkpoint carries no replay inputs")
	}
	return nil
}

// validCheckpointID is the shape of a checkpoint identity: 64 lowercase hex
// digits. It is also what makes it safe as a file name.
func validCheckpointID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// EncodeCheckpoint is the one canonical encoding of a checkpoint. A checkpoint
// whose framing is not closed and typed is refused before it is encoded.
func EncodeCheckpoint(c Checkpoint) ([]byte, error) {
	if err := c.checkFraming(); err != nil {
		return nil, err
	}
	return json.Marshal(c)
}

// DecodeCheckpoint reads exactly one complete canonical checkpoint and then
// the end of the payload. A trailing bracket, brace, second value or any other
// non-whitespace byte is malformed, and so is a payload that is not the
// canonical encoding of what it decodes to.
func DecodeCheckpoint(raw []byte) (Checkpoint, error) {
	var c Checkpoint
	if err := DecodeExactlyOne(raw, &c); err != nil {
		return Checkpoint{}, fmt.Errorf("the checkpoint payload is malformed: %w", err)
	}
	if err := c.checkFraming(); err != nil {
		return Checkpoint{}, err
	}
	canonical, err := json.Marshal(c)
	if err != nil {
		return Checkpoint{}, err
	}
	if !bytes.Equal(canonical, bytes.TrimSpace(raw)) {
		return Checkpoint{}, errors.New("the checkpoint payload is not the canonical encoding of the checkpoint it decodes to")
	}
	return c, nil
}

// DecodeExactlyOne decodes one JSON value into v, refusing an unknown field,
// and then requires the end of raw: only whitespace may follow the value. The
// end is established from the decoder's own offset into raw, never by asking
// the decoder whether more values follow.
func DecodeExactlyOne(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if rest := bytes.TrimSpace(raw[dec.InputOffset():]); len(rest) != 0 {
		return fmt.Errorf("%d byte(s) follow the value", len(rest))
	}
	return nil
}

// CheckpointBinding is what a CHECKPOINT_PREPARED and a CHECKPOINT_COMMITTED
// record each carry. A commit is authoritative only for the prepare that binds
// exactly the same values.
type CheckpointBinding struct {
	CheckpointID         string           `json:"checkpoint_id"`
	PreviousCheckpointID string           `json:"previous_checkpoint_id,omitempty"`
	TaskID               string           `json:"task_id"`
	PlanAttemptID        string           `json:"plan_attempt_id"`
	Status               CheckpointStatus `json:"status"`
	Retirement           RetirementReason `json:"retirement,omitempty"`
	PayloadDigest        string           `json:"payload_digest"`
	ReplayDigest         string           `json:"replay_digest"`
}

// CheckpointRecord is one prepared or committed record as the session holds
// it. Source and SessionID are the event's own, kept because ownership is
// proven from them.
type CheckpointRecord struct {
	Kind      event.Kind
	Source    event.Source
	SessionID string
	TaskID    string
	Binding   CheckpointBinding
	// Malformed says why the record's binding could not be read; such a
	// record binds nothing.
	Malformed string
}

// CheckpointRecords projects every prepared and committed record of taskID,
// in record order, with the Source and SessionID each was written under.
func CheckpointRecords(events []event.Event, taskID string) []CheckpointRecord {
	var out []CheckpointRecord
	for _, e := range events {
		if e.TaskID != taskID || (e.Kind != event.CheckpointPrepared && e.Kind != event.CheckpointCommitted) {
			continue
		}
		r := CheckpointRecord{Kind: e.Kind, Source: e.Source, SessionID: e.SessionID, TaskID: e.TaskID}
		if err := DecodeExactlyOne(e.Payload, &r.Binding); err != nil {
			r.Malformed = err.Error()
		} else if !r.Binding.Status.Valid() || (r.Binding.Status == CheckpointRetired) != (r.Binding.Retirement != "") ||
			(r.Binding.Retirement != "" && !r.Binding.Retirement.Valid()) {
			r.Malformed = fmt.Sprintf("status %q with retirement %q is not a durable checkpoint state", r.Binding.Status, r.Binding.Retirement)
		}
		out = append(out, r)
	}
	return out
}

// owns reports whether r is an engine-sourced record of taskID written under
// sessionID that names taskID itself.
func (r CheckpointRecord) owns(taskID, sessionID string) bool {
	return r.Malformed == "" && r.Source == event.SourceSystem && r.SessionID == sessionID &&
		r.TaskID == taskID && r.Binding.TaskID == taskID && validCheckpointID(r.Binding.CheckpointID) &&
		r.Binding.PayloadDigest != "" && r.Binding.ReplayDigest != ""
}

// CommittedCheckpoint is the newest committed checkpoint of taskID in this
// session whose ownership is proven: an engine-sourced COMMITTED record and an
// EARLIER engine-sourced PREPARED record, both written under sessionID for
// taskID, binding exactly the same checkpoint, plan attempt, status, payload
// digest and ReplayDigest. A commit that cannot prove this is not a committed
// checkpoint, and an older proven one stays the newest.
func CommittedCheckpoint(events []event.Event, taskID, sessionID string) (CheckpointBinding, bool) {
	var prepared []CheckpointBinding
	var found CheckpointBinding
	ok := false
	for _, r := range CheckpointRecords(events, taskID) {
		if !r.owns(taskID, sessionID) {
			continue
		}
		switch r.Kind {
		case event.CheckpointPrepared:
			prepared = append(prepared, r.Binding)
		case event.CheckpointCommitted:
			for _, p := range prepared {
				if p == r.Binding {
					found, ok = r.Binding, true
					break
				}
			}
		}
	}
	return found, ok
}

// PreparedAt is the record boundary a checkpoint was prepared at: the index in
// events of the first engine-sourced PREPARED record, written under sessionID,
// that binds exactly b. The record before that index is the durable state the
// checkpoint was constructed from, and the only state its replay may read:
// nothing recorded afterwards can establish it retroactively.
func PreparedAt(events []event.Event, b CheckpointBinding, sessionID string) (int, bool) {
	for i, e := range events {
		if e.Kind != event.CheckpointPrepared || e.TaskID != b.TaskID {
			continue
		}
		r := CheckpointRecords(events[i:i+1], b.TaskID)
		if len(r) == 1 && r[0].owns(b.TaskID, sessionID) && r[0].Binding == b {
			return i, true
		}
	}
	return 0, false
}

// CommittedCheckpoint reads this session's record and returns the newest
// committed checkpoint of taskID owned by sessionID. found=false with a nil error is ABSENCE,
// established by a successful read; an error means the record could not be
// read and whether an earlier checkpoint exists is UNKNOWN.
func (s *Store) CommittedCheckpoint(taskID, sessionID string) (CheckpointBinding, bool, error) {
	history, err := s.ReadRecord()
	if err != nil {
		return CheckpointBinding{}, false, err
	}
	b, ok := CommittedCheckpoint(history, taskID, sessionID)
	return b, ok, nil
}

// ReadRecord is an authoritative read of this session's record. No record
// file is a session that has written nothing: a successful read of an empty
// history. Any other failure is an error, and what the record holds -- an
// earlier checkpoint included -- is then unknown.
func (s *Store) ReadRecord() ([]event.Event, error) {
	history, err := s.Load()
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("the session record could not be read, so whether an earlier "+
			"checkpoint exists is unknown: %w", err)
	}
	return history, nil
}

// WriteCheckpoint durably writes one checkpoint payload under its id. It is
// written to a temporary file, synced and renamed into place, so a payload
// that exists is a complete one; an existing payload is never overwritten.
func (s *Store) WriteCheckpoint(id string, payload []byte) error {
	if !validCheckpointID(id) {
		return fmt.Errorf("checkpoint id %q is not a checkpoint identity", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.checkpointDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	final := filepath.Join(dir, id+".json")
	if _, err := os.Stat(final); err == nil {
		return fmt.Errorf("checkpoint %s is already written; a checkpoint payload is never overwritten", id)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(dir, id+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		return err
	}
	// The rename is durable only once the directory that names it is.
	return syncDir(dir)
}

// AppendDurable appends one event and does not return until it is on stable
// storage: written, synced and closed, every error returned, and the record's
// directory synced so a record file it created is named durably too. A
// checkpoint's PREPARED and COMMITTED records are written through it, because
// what they publish is authority, and an Append that a crash can lose is not.
func (s *Store) AppendDurable(e event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(e); err != nil {
		return err
	}
	if token := buf.Len() - 1; token > maxSessionEvent {
		return fmt.Errorf("session event for task %q is %d bytes, over the %d-byte maximum a single "+
			"event may occupy; it is refused before it is written", e.TaskID, token, maxSessionEvent)
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return syncDir(filepath.Dir(s.path))
}

// syncDir makes the entries of dir durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		d.Close()
		return err
	}
	return d.Close()
}

// ReadCheckpoint reads one checkpoint payload back, byte for byte.
func (s *Store) ReadCheckpoint(id string) ([]byte, error) {
	if !validCheckpointID(id) {
		return nil, fmt.Errorf("checkpoint id %q is not a checkpoint identity", id)
	}
	return os.ReadFile(filepath.Join(s.checkpointDir(), id+".json"))
}

func (s *Store) checkpointDir() string {
	return filepath.Join(filepath.Dir(s.path), "checkpoints")
}

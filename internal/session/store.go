package session

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/globulario/sensei-code/internal/authority"
	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
)

type Store struct {
	// gate is this Store's own serialization of every record operation --
	// root validation, lineage binding, append authorization and commit --
	// taken by lockRecord under the same bounded, cancellable wait as the
	// record's OS lock, never unconditionally; created once (gateOnce).
	gateOnce sync.Once
	gate     chan struct{}
	// checkpointMu serializes checkpoint payload writes, which touch neither
	// the record nor its cache.
	checkpointMu sync.Mutex
	path         string
	// cache is the record as last read or written under the record lock
	// (lockedRecord); guarded by gate.
	cache *recordCache
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

// Append appends one event of a task to the record. Its writer presents
// lease, the WRITE CAPABILITY: the task's live invocation lease, taken through
// a Store of this physical record and made operative for exactly the event's
// session by the Store itself (CreateTaskRoot, BindSessionLineage or
// ContinueTaskSession). Under the record lock the capability is verified, the
// task's session lineage authorizes the writer (authorizeAppend) against the
// record as it stands, and only then is the event written; a refusal writes
// nothing. A visible SessionID is not the capability: no lease, a released
// one, one of another task or record, or one operative for another session
// appends nothing, even for the lineage's tip. A task's root is created only
// by CreateTaskRoot, and a record of no task only by AppendDiagnostic. Its
// wait for the record lock is the caller's: it ends, refused and typed, when
// ctx does (ErrRecordLockCanceled) and in any case after recordLockWait
// (ErrRecordLockTimeout). No append path supplies a context of its own.
func (s *Store) Append(ctx context.Context, lease *TaskLease, e event.Event) error {
	return s.appendAuthorized(ctx, lease, false, e)
}

// encodeBounded encodes e onto buf as the one line Load will read back, and
// refuses it -- ErrSessionEventTooLarge, with buf as it was -- when that line
// is over the ceiling Load reads.
func encodeBounded(buf *bytes.Buffer, e event.Event) ([]byte, error) {
	at := buf.Len()
	if err := json.NewEncoder(buf).Encode(e); err != nil {
		buf.Truncate(at)
		return nil, err
	}
	// Encode appends the newline delimiter. Scanner's ceiling applies to the TOKEN,
	// which excludes that byte, so the token is what must fit -- measured rather than
	// assumed, because an off-by-one here is precisely the boundary that would make
	// Append and Load disagree again.
	line := buf.Bytes()[at:]
	if token := len(line) - 1; token > maxSessionEvent {
		buf.Truncate(at)
		return nil, fmt.Errorf("%w: session event for task %q is %d bytes, over the %d-byte maximum a single "+
			"event may occupy; it is refused before it is written, because a durable record that "+
			"Load cannot read back is worse than a rejected append", ErrSessionEventTooLarge, e.TaskID, token, maxSessionEvent)
	}
	return line, nil
}

// AppendUnit appends evs as ONE semantic unit, durably and ALL OR NOTHING: a
// run's receipt and the terminal it accounts for, which must never be
// recorded one without the other. Each event is encoded and bounded first;
// then, under the record lock, each is authorized (authorizeAppend) against
// the record as it stands extended by the events of the unit before it; and
// only then are all of them written in one call and synced
// (commitRecordAppend). A refusal of any of them, or a failure of the write,
// leaves the record byte for byte as it was, and no event of the unit is
// recorded. Its wait for the record lock is the caller's ctx, and its write
// capability the caller's lease, as Append's: every event of the unit is
// verified against it.
func (s *Store) AppendUnit(ctx context.Context, lease *TaskLease, evs ...event.Event) error {
	if len(evs) == 0 {
		return fmt.Errorf("%w: an append unit names no event", ErrSessionAppendRefused)
	}
	return s.appendAuthorized(ctx, lease, true, evs...)
}

// appendAuthorized is every generic append of a task's events, of one event or
// of one unit: it encodes and bounds each event, takes the record lock
// (bounded, and ended by ctx), verifies the writer's capability (lease) and
// authorizes each event's writer against the record under that lock -- the
// record as it stands, extended by the unit's earlier events -- and only then
// writes them all in one call.
func (s *Store) appendAuthorized(ctx context.Context, lease *TaskLease, durable bool, evs ...event.Event) error {
	if s == nil {
		return fmt.Errorf("%w: nothing was appended", ErrNoStore)
	}
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
	written := make([]event.Event, 0, len(evs))
	for _, e := range evs {
		// Refused before the lock is taken: none is a generic append.
		switch {
		case e.Kind == SessionLineageBound:
			return authorizeAppend(nil, e)
		case e.TaskID == "":
			return fmt.Errorf("%w: a %s record of no task is a diagnostic, appended only by AppendDiagnostic",
				ErrSessionAppendRefused, e.Kind)
		case e.Kind == event.TaskCreated:
			return fmt.Errorf("%w: the TaskCreated root of task %s is created only by CreateTaskRoot",
				ErrSessionAppendRefused, e.TaskID)
		}
		line, err := encodeBounded(&buf, e)
		if err != nil {
			return err
		}
		// Authorized as Load will read it back, not as the caller built it.
		var w event.Event
		if err := json.Unmarshal(line, &w); err != nil {
			return err
		}
		written = append(written, w)
	}

	// THE WRITE CAPABILITY is admitted first (guardTaskLease) and held until
	// the append is committed or undone, so the lease that authorizes the
	// write is the lease still held when it is written.
	end, err := s.guardTaskLease(lease, written[0].TaskID)
	if err != nil {
		w := written[0]
		return fmt.Errorf("%w: a %s record of task %s by session %s: %w",
			ErrSessionAppendRefused, w.Kind, w.TaskID, orNoSession(w.SessionID), err)
	}
	defer end()
	release, err := s.lockRecord(ctx)
	if err != nil {
		return err
	}
	defer release()
	record, err := s.lockedRecord()
	if err != nil {
		return err
	}
	staged := record[:len(record):len(record)]
	for _, w := range written {
		// Every event of the unit is of the guarded lease's task and record,
		// and the lease is operative for exactly its session once the lineage
		// authorizes it.
		if err := s.leaseIdentity(lease, w.TaskID); err != nil {
			return fmt.Errorf("%w: a %s record of task %s by session %s: %w",
				ErrSessionAppendRefused, w.Kind, w.TaskID, orNoSession(w.SessionID), err)
		}
		if err := authorizeAppend(staged, w); err != nil {
			return err
		}
		if err := lease.operativeFor(w.SessionID); err != nil {
			return fmt.Errorf("%w: a %s record of task %s: %w", ErrSessionAppendRefused, w.Kind, w.TaskID, err)
		}
		staged = append(staged, w)
	}
	if err := s.commitRecordAppend(buf.Bytes(), durable); err != nil {
		s.cache = nil
		return err
	}
	s.remember(staged)
	return nil
}

// AppendDiagnostic appends one record of NO task: a diagnostic of the closed
// taskless registry (ValidDiagnosticEvent), which carries no task authority
// and so needs no task's capability. It is the one append path of a record of
// no task, kept apart from every task-bound one; a record of a task, of any
// kind, is refused here and written nothing. Its wait for the record lock is
// the caller's ctx, as Append's.
func (s *Store) AppendDiagnostic(ctx context.Context, e event.Event) error {
	if s == nil {
		return fmt.Errorf("%w: nothing was appended", ErrNoStore)
	}
	if e.TaskID != "" {
		return fmt.Errorf("%w: a %s record of task %s is not a diagnostic", ErrSessionAppendRefused, e.Kind, e.TaskID)
	}
	var buf bytes.Buffer
	line, err := encodeBounded(&buf, e)
	if err != nil {
		return err
	}
	var w event.Event
	if err := json.Unmarshal(line, &w); err != nil {
		return err
	}
	release, err := s.lockRecord(ctx)
	if err != nil {
		return err
	}
	defer release()
	record, err := s.lockedRecord()
	if err != nil {
		return err
	}
	if err := authorizeAppend(record, w); err != nil {
		return err
	}
	if err := s.commitRecordAppend(line, false); err != nil {
		s.cache = nil
		return err
	}
	s.remember(append(record[:len(record):len(record)], w))
	return nil
}

// CreateTaskRoot is the ONE writer of a task's TaskCreated root: the dedicated,
// lease-bearing creation of a task's ledger. Its caller presents the task's
// live invocation lease, taken through a Store of this physical record and
// not yet operative for any session. Under the record lock it verifies that
// lease, refuses a root step does not admit (authorizeAppend) -- a malformed
// root, or a second one -- and proves, under the same lease every root of the
// task in the repository must be created under, that no other record claims
// the task (soleTaskLedger); only then is the root written, durably. So root
// uniqueness is checked and the root created with no window between them in
// which another creator can act. The lease is then operative for the root's
// session: the holder's generic appends are authorized under it.
func (s *Store) CreateTaskRoot(ctx context.Context, lease *TaskLease, e event.Event) error {
	if s == nil {
		return fmt.Errorf("%w: task %s", ErrNoStore, e.TaskID)
	}
	if e.Kind != event.TaskCreated {
		return fmt.Errorf("%w: a %s record of task %s is not a task root", ErrSessionAppendRefused, e.Kind, e.TaskID)
	}
	// The caller's identity, before encoding: JSON would replace invalid
	// UTF-8 and so make a noncanonical identity look canonical.
	if err := canonicalTaskID(e.TaskID); err != nil {
		return fmt.Errorf("%w: the TaskCreated root of task %q: %w", ErrSessionAppendRefused, e.TaskID, err)
	}
	var buf bytes.Buffer
	line, err := encodeBounded(&buf, e)
	if err != nil {
		return err
	}
	var w event.Event
	if err := json.Unmarshal(line, &w); err != nil {
		return err
	}
	end, err := s.guardTaskLease(lease, w.TaskID)
	if err != nil {
		return fmt.Errorf("%w: the TaskCreated root of task %s: %w", ErrSessionAppendRefused, w.TaskID, err)
	}
	defer end()
	release, err := s.lockRecord(ctx)
	if err != nil {
		return err
	}
	defer release()
	if op := lease.operative(); op != "" {
		return fmt.Errorf("%w: the TaskCreated root of task %s: %w: the lease is already operative for session %s",
			ErrSessionAppendRefused, w.TaskID, ErrTaskLeaseRequired, op)
	}
	record, err := s.lockedRecord()
	if err != nil {
		return err
	}
	if err := authorizeAppend(record, w); err != nil {
		return err
	}
	if err := s.soleTaskLedger(ctx, w.TaskID, true); err != nil {
		return fmt.Errorf("%w: the TaskCreated root of task %s: %w", ErrSessionAppendRefused, w.TaskID, err)
	}
	if err := s.commitRecordAppend(line, true); err != nil {
		s.cache = nil
		return err
	}
	s.remember(append(record[:len(record):len(record)], w))
	lease.makeOperative(w.SessionID)
	return nil
}

// ContinueTaskSession makes lease operative for sessionID when sessionID is
// ALREADY its task's current session: the process that holds the task
// continuing it again under the same session, with no transition to write.
// Under the record lock it verifies the live lease of the task and record and
// requires the lineage, proven from the record's root, to name sessionID as
// its tip. Any other session -- stale, foreign, unbound -- is refused, and is
// made current only by BindSessionLineage.
func (s *Store) ContinueTaskSession(ctx context.Context, lease *TaskLease, sessionID string) (SessionLineage, error) {
	if s == nil {
		return SessionLineage{}, fmt.Errorf("%w: task %s", ErrNoStore, lease.TaskID())
	}
	taskID := lease.TaskID()
	end, err := s.guardTaskLease(lease, taskID)
	if err != nil {
		return SessionLineage{}, err
	}
	defer end()
	release, err := s.lockRecord(ctx)
	if err != nil {
		return SessionLineage{}, err
	}
	defer release()
	record, err := s.lockedRecord()
	if err != nil {
		return SessionLineage{}, err
	}
	l, err := TaskSessionLineage(record, taskID)
	if err != nil {
		return SessionLineage{}, err
	}
	if strings.TrimSpace(sessionID) == "" || l.Tip() != sessionID {
		return SessionLineage{}, fmt.Errorf("%w: session %s is not task %s's current session, which is %s; it continues nothing",
			ErrSessionLineage, orNoSession(sessionID), taskID, l.Tip())
	}
	if op := lease.operative(); op != "" && op != sessionID {
		return SessionLineage{}, fmt.Errorf("%w: the lease is already operative for session %s", ErrTaskLeaseRequired, op)
	}
	lease.makeOperative(sessionID)
	return l, nil
}

// payloadMembers is a governed event's payload decoded as one JSON object,
// its members undecoded: what the kind registry (governedKinds) reads.
type payloadMembers map[string]json.RawMessage

// decodePayload decodes raw as one JSON object. An absent payload -- empty or
// null -- is present=false with no members; any other value that is not one
// object is an error.
func decodePayload(raw json.RawMessage) (m payloadMembers, present bool, err error) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || string(t) == "null" {
		return nil, false, nil
	}
	if t[0] != '{' {
		return nil, true, errors.New("the payload is not a JSON object")
	}
	if err := json.Unmarshal(t, &m); err != nil {
		return nil, true, err
	}
	return m, true, nil
}

func (m payloadMembers) has(key string) bool {
	_, ok := m[key]
	return ok
}

// str is member key as a string; ok is false when it is absent or not one.
func (m payloadMembers) str(key string) (string, bool) {
	raw, ok := m[key]
	if !ok {
		return "", false
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return "", false
	}
	return v, true
}

// object reports whether member key is present as a JSON object.
func (m payloadMembers) object(key string) bool {
	t := bytes.TrimSpace(m[key])
	return len(t) > 0 && t[0] == '{'
}

// list reports whether member key is present as a JSON array or null.
func (m payloadMembers) list(key string) bool {
	t := bytes.TrimSpace(m[key])
	return len(t) > 0 && (t[0] == '[' || string(t) == "null")
}

// member is member key decoded as an object, with no members when it is
// absent or not one.
func (m payloadMembers) member(key string) payloadMembers {
	sub, _, err := decodePayload(m[key])
	if err != nil {
		return nil
	}
	return sub
}

// number is member key as a JSON number; ok is false when it is absent or
// not one.
func (m payloadMembers) number(key string) (v float64, ok bool) {
	raw, present := m[key]
	if !present || json.Unmarshal(raw, &v) != nil {
		return 0, false
	}
	return v, true
}

// boolean is member key as a JSON boolean; ok is false when it is absent or
// not one.
func (m payloadMembers) boolean(key string) (v bool, ok bool) {
	raw, present := m[key]
	if !present || json.Unmarshal(raw, &v) != nil {
		return false, false
	}
	return v, true
}

// integer is member key as a JSON integer; ok is false when it is absent or
// not one.
func (m payloadMembers) integer(key string) (v int64, ok bool) {
	raw, present := m[key]
	if !present || json.Unmarshal(raw, &v) != nil {
		return 0, false
	}
	return v, true
}

// strs is member key as a JSON array of strings; ok is false when it is
// absent, null or not one.
func (m payloadMembers) strs(key string) (v []string, ok bool) {
	t := bytes.TrimSpace(m[key])
	if len(t) == 0 || t[0] != '[' || json.Unmarshal(t, &v) != nil {
		return nil, false
	}
	return v, true
}

// objects is member key as a JSON array of objects; ok is false when it is
// absent, null, not an array, or holds anything but objects.
func (m payloadMembers) objects(key string) ([]payloadMembers, bool) {
	t := bytes.TrimSpace(m[key])
	var raws []json.RawMessage
	if len(t) == 0 || t[0] != '[' || json.Unmarshal(t, &raws) != nil {
		return nil, false
	}
	out := make([]payloadMembers, 0, len(raws))
	for _, raw := range raws {
		sub, present, err := decodePayload(raw)
		if err != nil || !present {
			return nil, false
		}
		out = append(out, sub)
	}
	return out, true
}

func validRole(r string) bool { return roles.Role(r).Valid() }

// validInstant is a time recorded as RFC 3339, the form every governed record
// states one in.
func validInstant(s string) bool {
	_, err := time.Parse(time.RFC3339, s)
	return err == nil
}

// validAuthorityLevel reads a recorded decision's level by membership in
// authority's closed level vocabulary.
func validAuthorityLevel(l int64) bool {
	switch authority.Level(l) {
	case authority.Execution, authority.Architectural, authority.Human:
		return l >= 0 && l <= int64(authority.Human)
	}
	return false
}

func validReviewDecision(d string) bool { return roles.Decision(d).Valid() }

func validReconciliationAuthority(a string) bool { return roles.Level(a).Valid() }

func validFindingSeverity(s string) bool { return roles.Severity(s).Valid() }

func validAuthorityOutcome(o string) bool { return authority.Outcome(o).Valid() }

// candidateResolutionPayload is the CandidateResolved record (RULING-200 A):
// exactly one candidate.Resolution, every member it declares present --
// including both removal observations -- and nothing else, its evidence
// exactly candidate.Evidence. The resolution is one the candidate owner may
// record (Resolution.Validate), decided at a stated time; a retaining
// disposition reports no removal, a branch is never removed without its
// worktree, and the record's summary is the resolution's own. The record is
// the disposition owner's, Git's (governedKinds), whose decision it is.
func candidateResolutionPayload(e event.Event, m payloadMembers, present bool) error {
	if err := exactlyOneOf(e, m, present, candidateResolutionShape); err != nil {
		return err
	}
	if err := exactlyOneOf(e, m.member("evidence"), true, candidateEvidenceShape); err != nil {
		return err
	}
	var r candidate.Resolution
	if err := DecodeExactlyOne(e.Payload, &r); err != nil {
		return fmt.Errorf("%w: the candidate resolution of task %s does not decode: %v", ErrMalformedEvent, e.TaskID, err)
	}
	switch err := r.Validate(); {
	case err != nil:
		return fmt.Errorf("%w: the candidate resolution of task %s is not one its owner may record: %v", ErrMalformedEvent, e.TaskID, err)
	case r.DecidedAt.IsZero():
		return fmt.Errorf("%w: the candidate resolution of task %s states no decision time", ErrMalformedEvent, e.TaskID)
	case r.Disposition.Keeps() && (r.WorktreeRemoved || r.BranchRemoved):
		return fmt.Errorf("%w: the candidate resolution of task %s retains the candidate (%s) and reports it removed", ErrMalformedEvent, e.TaskID, r.Disposition)
	case r.BranchRemoved && !r.WorktreeRemoved:
		return fmt.Errorf("%w: the candidate resolution of task %s removes the branch and not its worktree", ErrMalformedEvent, e.TaskID)
	case e.Summary != r.Summary():
		return fmt.Errorf("%w: the candidate resolution of task %s is summarized as %q, not as its resolution %q", ErrMalformedEvent, e.TaskID, e.Summary, r.Summary())
	}
	return nil
}

// candidateResolutionShape and candidateEvidenceShape are candidate.Resolution
// and candidate.Evidence as they marshal: the members each always carries are
// required, the omitempty ones optional.
var (
	candidateResolutionShape = shape{name: "a candidate resolution", members: map[string]member{
		"disposition": req(tText), "reason": req(tText), "decided_at": req(tText), "evidence": req(tObject),
		"worktree_removed": req(tBool), "branch_removed": req(tBool),
	}}
	candidateEvidenceShape = shape{name: "candidate evidence", members: map[string]member{
		"base_sha": req(tString), "diff_digest": opt(tString), "diff_bytes": req(tNumber), "changed_paths": opt(tStrings),
		"audit_verdict": opt(tString), "audit_detail": opt(tString), "executed_checks": opt(tStrings), "produced_no_work": opt(tBool),
	}}
)

// validResolutionState reads a recorded resolution's persistence state by
// membership in authority's closed vocabulary.
func validResolutionState(st string) bool {
	switch authority.PersistenceState(st) {
	case authority.Proposed, authority.Unsupported, authority.Rejected, authority.Failed:
		return true
	}
	return false
}

// ErrAppendIndeterminate is a generic append whose write could neither be
// completed and verified nor undone: the record may hold all, part or none
// of the event, so what it holds is unknown. It is never reported as an
// event that was not recorded.
var ErrAppendIndeterminate = errors.New("the append could neither be committed nor undone")

// appendCommitFault is consulted at each step of a generic append's commit
// (commitRecordAppend): "write", once its bytes are written; "sync", in
// place of the sync of a durable append; and "rollback", before a failed
// append is undone. A non-nil error fails that step. It is a variable only so
// a test can fail each step; production never replaces it.
var appendCommitFault = func(step string) error { return nil }

// commitRecordAppend appends lines -- complete encoded events -- to the record
// ALL OR NOTHING. Its callers hold the record lock. The record's length is
// taken first; the lines are written in one call, and a durable append syncs
// them and the record's directory; then the record must be exactly that
// length plus the lines. If any step fails -- a short or failed write, a
// failed sync, close or directory sync, a record of any other length -- the
// append is undone: the record is truncated back to its length and synced,
// or removed when this append created it, so it is byte for byte what it
// was, every earlier event intact, and the failure is returned as the
// event's not having been recorded. Only when that undo itself fails is the
// failure ErrAppendIndeterminate: what the record holds is then unknown.
func (s *Store) commitRecordAppend(lines []byte, durable bool) error {
	var size int64
	info, err := os.Stat(s.path)
	existed := err == nil
	switch {
	case existed:
		size = info.Size()
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		if existed {
			return err
		}
		return s.undoAppend(size, existed, err)
	}
	n, err := f.Write(lines)
	if err == nil && n != len(lines) {
		err = fmt.Errorf("short write: %d of %d bytes", n, len(lines))
	}
	if err == nil {
		err = appendCommitFault("write")
	}
	if err == nil && durable {
		// The injected "sync" failure stands in for the sync itself.
		if err = appendCommitFault("sync"); err == nil {
			err = f.Sync()
		}
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && durable {
		err = syncDir(filepath.Dir(s.path))
	}
	if err == nil {
		if info, serr := os.Stat(s.path); serr != nil {
			err = serr
		} else if want := size + int64(len(lines)); info.Size() != want {
			err = fmt.Errorf("the record is %d bytes after an append that should leave it %d", info.Size(), want)
		}
	}
	if err != nil {
		return s.undoAppend(size, existed, err)
	}
	return nil
}

// undoAppend undoes a generic append whose commit failed with cause: a record
// that existed is truncated back to size and synced, and one the append
// created is removed and its directory synced. Undone, the append is cause:
// the event was not recorded. Not undone, it is ErrAppendIndeterminate. Its
// callers hold the record lock.
func (s *Store) undoAppend(size int64, existed bool, cause error) error {
	undo := appendCommitFault("rollback")
	if undo == nil {
		if existed {
			undo = truncateRecord(s.path, size)
		} else if undo = os.Remove(s.path); undo == nil || errors.Is(undo, os.ErrNotExist) {
			undo = syncDir(filepath.Dir(s.path))
		}
	}
	if undo != nil {
		return fmt.Errorf("%w: the append failed (%w), and the record could not be restored to its %d bytes (%v); "+
			"whether it holds the event is unknown", ErrAppendIndeterminate, cause, size, undo)
	}
	return fmt.Errorf("the append was not committed, and the record is restored to the %d bytes it held: %w", size, cause)
}

// recordCache is the record as this Store last read or wrote it under the
// record lock, and the file it was read from. It saves re-decoding the whole
// record for every append's authorization and is never authority of its own:
// lockedRecord trusts it only for the same file, extended by whatever another
// writer appended since, and rereads the record otherwise.
type recordCache struct {
	info   os.FileInfo
	events []event.Event
}

// lockedRecord is the record as it stands now. Its callers hold the record
// lock (lockRecord), so no writer that takes the lock can change it underneath.
func (s *Store) lockedRecord() ([]event.Event, error) {
	info, err := os.Stat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.cache = nil
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if c := s.cache; c != nil && os.SameFile(c.info, info) {
		switch {
		case info.Size() == c.info.Size() && info.ModTime().Equal(c.info.ModTime()):
			return c.events, nil
		case info.Size() > c.info.Size():
			if tail, err := readEventsFrom(s.path, c.info.Size()); err == nil {
				events := append(c.events[:len(c.events):len(c.events)], tail...)
				s.cache = &recordCache{info: info, events: events}
				return events, nil
			}
		}
	}
	s.cache = nil
	events, err := s.ReadRecord()
	if err != nil {
		return nil, err
	}
	s.cache = &recordCache{info: info, events: events}
	return events, nil
}

// remember records events as the record this Store just wrote.
func (s *Store) remember(events []event.Event) {
	info, err := os.Stat(s.path)
	if err != nil {
		s.cache = nil
		return
	}
	s.cache = &recordCache{info: info, events: events}
}

// readEventsFrom decodes the events of the record at path from byte offset
// off, which is the end of a complete event, exactly as Load decodes them.
func readEventsFrom(path string, off int64) ([]event.Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// Whence 0 is the start of the file.
	if _, err := f.Seek(off, 0); err != nil {
		return nil, err
	}
	var out []event.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, initialSessionEventBuffer), maxSessionEvent+1)
	for sc.Scan() {
		var e event.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, sc.Err()
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

// FreshID mints the SessionID of a fresh process that continues an existing
// session record: the instant's ID joined to 128 bits from crypto/rand. The
// clock alone is not an identity -- two processes resuming in the same instant
// would share it -- so an unavailable entropy source refuses rather than
// falling back to it.
func FreshID(t time.Time) (string, error) {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", fmt.Errorf("no entropy is available to mint a fresh session identity: %w", err)
	}
	return ID(t) + "-" + hex.EncodeToString(entropy[:]), nil
}

// readableSessions is every ORDINARY, REUSABLE session record this repository
// holds, oldest first: the records a conversation may be continued in.
//
// ONE definition of what counts as a record, because two of them disagreed. The
// question "is this session readable" is asked by Latest and by FindActive, and
// each had its own answer; both treated EVERY stat error as "no record here", so
// a permission failure, an I/O error or a symlink loop SHRANK the world being
// searched instead of stopping the search. A history nobody could open was
// reported as a history that holds nothing. So it is read from the one task-claim
// inventory admission and discovery share (claimInventory), not from a listing
// of its own.
//
// A record is skipped ONLY when it does not exist -- a session directory created
// and never written to, which is absence, and answerable -- or when a valid
// quarantine manifest excludes it. A quarantined record stays authoritative
// evidence: its claims stay reserved at admission and it stays visible to
// discovery (FindActive) and the operator (QuarantineStatus); it is simply never
// an ordinary conversation to continue. Every other failure -- a record that
// cannot be read and is not validly quarantined, a malformed, stale or orphan
// manifest -- is returned, typed, because "I could not look" is not "there is
// nothing there".
//
// Session ids sort chronologically (see ID), so the result reads in the order
// things happened.
func readableSessions(repo string) ([]string, error) {
	// No caller context reaches selection; the locked re-read of a damaged
	// record is bounded by recordLockWait alone.
	records, err := claimInventory(context.Background(), filepath.Join(repo, ".sensei-code", "sessions"), "")
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, r := range records {
		if r.quarantine != nil {
			continue
		}
		ids = append(ids, r.sessionID)
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
//
// It is the newest ORDINARY record (readableSessions): a validly quarantined
// record is never reopened as a conversation, however recent, and a damaged
// record no valid manifest excludes is a typed refusal, never skipped.
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
	// Unavailable is set, by CanonicalInterrupted, when the task's history in
	// this record cannot be projected onto its canonical session lineage
	// (ProjectTaskHistory): a
	// record of it is malformed, or the record holds records of it and no
	// lineage member. The task is then REPORTED, never skipped, and nothing
	// else here is reconstructed for it -- every other field but TaskID is
	// empty, and empty is not what it owes. No continuation consumes it:
	// every lane refuses it, typed, before ordinary lane selection.
	Unavailable *TaskHistoryRefusal
}

// THE CANONICAL MEMBER-HISTORY PROJECTION (70B2a2, RULING-216/219/220).
//
// A holder ledger may hold records of a task that are correctly framed and
// still not the task's: written by an unrelated, unbound or superseded
// session, or before the task's root. They are historical evidence and stay
// in the ledger exactly as written. They are never authority: no resume,
// discovery, authority, plan, grant, gap or terminal state is decided from
// them. ProjectTaskHistory is the one reader that says which records of a
// task its operative state may be decided from -- the HistoryLineageMember
// records ClassifyTaskHistory admits -- and every authority-bearing history
// consumer reads through it (CanonicalInterrupted, FindActive,
// Store.TaskHistory).

// TaskHistoryUnavailableReason is why a task's history cannot be projected.
// The vocabulary is closed and read by membership.
type TaskHistoryUnavailableReason string

const (
	// TaskHistoryMalformed: a record of the task -- before its root included
	// -- is not a valid governed event, or its lineage cannot be established.
	TaskHistoryMalformed TaskHistoryUnavailableReason = "malformed"
	// TaskHistoryForeignOnly: the record holds records of the task and no
	// root of it, so none of them is a lineage member.
	TaskHistoryForeignOnly TaskHistoryUnavailableReason = "foreign_only"
	// TaskHistoryUnreadable: the record that holds the task could not be read.
	TaskHistoryUnreadable TaskHistoryUnavailableReason = "unreadable"
	// TaskHistoryQuarantined: a quarantined session record claims the task
	// (Discovery.QuarantinedTask); its identity is reserved and no record's
	// account of it is continued.
	TaskHistoryQuarantined TaskHistoryUnavailableReason = "quarantined"
)

// ErrTaskHistoryUnavailable is every TaskHistoryRefusal.
var ErrTaskHistoryUnavailable = errors.New("the task's history cannot be projected onto its session lineage")

// TaskHistoryRefusal is the typed per-task outcome of a history that cannot
// be projected. It is never an empty history: what the task owes is unknown,
// not nothing. The counts are diagnostic only; they never become authority
// and never stand in for membership.
type TaskHistoryRefusal struct {
	TaskID string
	Reason TaskHistoryUnavailableReason
	// At is the record position of the first malformed record, -1 when the
	// refusal names none.
	At     int
	Detail string
	// Members, Foreign, PreRoot and Malformed count the task's records by
	// class (ClassifyTaskHistory).
	Members, Foreign, PreRoot, Malformed int
	cause                                error
}

func (r *TaskHistoryRefusal) Error() string {
	return fmt.Sprintf("%v: task %s is %s (%d member, %d foreign, %d pre-root, %d malformed records): %s; "+
		"the task and its record are preserved and it is not continued", ErrTaskHistoryUnavailable, r.TaskID, r.Reason,
		r.Members, r.Foreign, r.PreRoot, r.Malformed, r.Detail)
}

// Unwrap is ErrTaskHistoryUnavailable and the cause, when one is known.
func (r *TaskHistoryRefusal) Unwrap() []error {
	if r.cause != nil {
		return []error{ErrTaskHistoryUnavailable, r.cause}
	}
	return []error{ErrTaskHistoryUnavailable}
}

// TaskHistory is a task's canonical member history in one holder record.
type TaskHistory struct {
	TaskID  string
	Lineage SessionLineage
	// Members are the task's HistoryLineageMember records, in record order,
	// and nothing else. Empty only when the record holds no record of the
	// task at all: a read that succeeded and found none.
	Members []event.Event
	// Foreign and PreRoot count the well-formed records of the task that are
	// not members. Diagnostic only: they are kept in the ledger and never
	// consumed.
	Foreign, PreRoot int
	// Recorded reports that Store.TaskHistory read an existing record file. A
	// reader whose contract does not permit a missing record to stand for an
	// empty one refuses on false.
	Recorded bool
}

// ProjectTaskHistory is the canonical member-history projection of taskID
// over record. A record holding no record of the task is a proven absence and
// projects an empty history. Any HistoryMalformed record of the task, a
// pre-root one included, and a lineage that cannot be established, are a
// TaskHistoryMalformed refusal; records of the task with no root are a
// TaskHistoryForeignOnly refusal. Foreign and pre-root records of a task with
// a valid lineage are excluded and counted, never erased.
func ProjectTaskHistory(record []event.Event, taskID string) (TaskHistory, error) {
	classified, lineage, err := ClassifyTaskHistory(record, taskID)
	if len(classified) == 0 && (err == nil || isNoTaskRoot(err)) {
		return TaskHistory{TaskID: taskID}, nil
	}
	refusal := &TaskHistoryRefusal{TaskID: taskID, At: -1}
	h := TaskHistory{TaskID: taskID, Lineage: lineage}
	for _, c := range classified {
		switch c.Class {
		case HistoryLineageMember:
			refusal.Members++
			h.Members = append(h.Members, c.Event)
		case HistoryForeign:
			refusal.Foreign++
		case HistoryPreRoot:
			// ClassifyTaskHistory leaves every record of a task with no root
			// pre-root, framed or not. A malformed one is malformed here: a
			// well-formed foreign record and a malformed one stay distinct.
			if ValidGovernedEvent(c.Event) == nil {
				refusal.PreRoot++
				break
			}
			fallthrough
		default:
			// Read by membership: anything not one of the three classes
			// above is malformed.
			refusal.Malformed++
			if refusal.At < 0 {
				refusal.At = c.Index
				if verr := ValidGovernedEvent(c.Event); verr != nil {
					refusal.cause = verr
				}
			}
		}
	}
	h.Foreign, h.PreRoot = refusal.Foreign, refusal.PreRoot
	switch {
	case refusal.Malformed != 0 || (err != nil && !isNoTaskRoot(err)):
		refusal.Reason = TaskHistoryMalformed
		if err != nil {
			refusal.cause = err
		}
		refusal.Detail = fmt.Sprintf("the record at position %d is malformed", refusal.At)
		if refusal.cause != nil {
			refusal.Detail += ": " + refusal.cause.Error()
		}
		return TaskHistory{}, refusal
	case err != nil:
		refusal.Reason, refusal.cause = TaskHistoryForeignOnly, err
		refusal.Detail = "the record holds records of the task and no root of it, so none of them is a lineage member"
		return TaskHistory{}, refusal
	}
	return h, nil
}

// TaskHistory is the canonical member history of taskID in this Store's
// record. Presence and contents come from ONE read of the record (Load): no
// record file is reported as Recorded=false with no members -- never as a
// record that was read -- and a record that exists and cannot be read is a
// TaskHistoryUnreadable refusal, never an empty history. A separate presence
// check would let a record that vanishes between it and the read report an
// empty history as Recorded.
func (s *Store) TaskHistory(taskID string) (TaskHistory, error) {
	record, err := s.Load()
	if errors.Is(err, os.ErrNotExist) {
		return TaskHistory{TaskID: taskID}, nil
	}
	if err != nil {
		return TaskHistory{}, &TaskHistoryRefusal{TaskID: taskID, Reason: TaskHistoryUnreadable, At: -1,
			Detail: "the session record could not be read: " + err.Error(), cause: err}
	}
	h, err := ProjectTaskHistory(record, taskID)
	h.Recorded = err == nil
	return h, err
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

// CanonicalInterrupted is THE resume and discovery projection of one holder
// record: every task that has begun and not ended, reconstructed from its
// canonical member history (ProjectTaskHistory) and nothing else. A correctly
// framed record of a task that a foreign, unbound or superseded session wrote,
// or one that precedes the task's root, neither creates, ends, asks, answers,
// plans nor grants anything here: it stays in the ledger as evidence. A task
// whose history cannot be projected is reported with its typed refusal
// (Interrupted.Unavailable) and nothing else -- never skipped, never read as
// empty, and never hiding another task of the record. Tasks are reported in
// the order the record first names them.
func CanonicalInterrupted(record []event.Event) []Interrupted {
	var order []string
	seen := map[string]bool{}
	for _, e := range record {
		if e.TaskID != "" && !seen[e.TaskID] {
			seen[e.TaskID] = true
			order = append(order, e.TaskID)
		}
	}
	var out []Interrupted
	for _, id := range order {
		h, err := ProjectTaskHistory(record, id)
		if err != nil {
			refusal := &TaskHistoryRefusal{TaskID: id, Reason: TaskHistoryMalformed, At: -1, Detail: err.Error(), cause: err}
			errors.As(err, &refusal)
			out = append(out, Interrupted{TaskID: id, Unavailable: refusal})
			continue
		}
		out = append(out, FindInterrupted(h.Members)...)
	}
	return out
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
//
// IT FOLDS THE RECORDS IT IS HANDED, ALL OF THEM, and is therefore not an
// authority over a holder ledger: a correctly framed record a foreign session
// wrote would fold here as if it were the task's. No production reader calls
// it on a record. CanonicalInterrupted is the projection every resume and
// discovery path reads, and it hands this fold one task's canonical member
// history (ProjectTaskHistory) at a time.
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
	// Quarantined is every record excluded from task reconstruction by a
	// valid quarantine manifest (claimInventory), oldest first. Its tasks are
	// visible here and never in Active: a quarantined task is never an
	// ordinary resumable one, and every identity it claims stays reserved.
	Quarantined []QuarantinedRecord
}

// QuarantinedRecord is one quarantined session record as discovery reports
// it: the session and the manifest that asserts its recovery.
type QuarantinedRecord struct {
	SessionID string
	Manifest  QuarantineManifest
}

// QuarantinedTask is the quarantined record that claims taskID, if one does:
// the typed discovery outcome of a task that is visible but never resumable.
func (d Discovery) QuarantinedTask(taskID string) (QuarantinedRecord, bool) {
	for _, q := range d.Quarantined {
		if slices.Contains(q.Manifest.Claims, taskID) {
			return q, true
		}
	}
	return QuarantinedRecord{}, false
}

// TaskRefusal is the ONE repository-wide refusal of taskID that every resume
// selection -- the CLI's and the TUI's alike -- reads before any scoped entry
// or lane: the first record holding the task with a history that cannot be
// projected onto its session lineage, and then a quarantined record claiming
// it. sessionID names the refusing record. A refusal from any record refuses
// the task everywhere: which account of it is the task's cannot be decided past
// it, and naming a session is a filter, never a waiver (ScopedTo). nil when
// nothing refuses the task.
func (d Discovery) TaskRefusal(taskID string) (sessionID string, refusal *TaskHistoryRefusal) {
	taskID = strings.TrimSpace(taskID)
	for _, entry := range d.Active {
		if entry.Task.TaskID == taskID && entry.Task.Unavailable != nil {
			return entry.SessionID, entry.Task.Unavailable
		}
	}
	if q, ok := d.QuarantinedTask(taskID); ok {
		return q.SessionID, &TaskHistoryRefusal{TaskID: taskID, Reason: TaskHistoryQuarantined, At: -1,
			Detail: "claimed by quarantined session record " + q.SessionID + "; its identity stays reserved",
			cause:  ErrTaskQuarantined}
	}
	return "", nil
}

// ResumableIn is what may be continued from ONE session record: ScopedTo's
// entries, each carrying the repository-wide TaskRefusal of its task when one
// exists, so a scoped view never offers a task another record refuses or a
// quarantine reserves. Refused entries stay visible, typed.
func (d Discovery) ResumableIn(sessionID string) ([]Active, error) {
	scoped, err := d.ScopedTo(sessionID)
	if err != nil {
		return nil, err
	}
	for i := range scoped {
		if _, refusal := d.TaskRefusal(scoped[i].Task.TaskID); refusal != nil {
			scoped[i].Task.Unavailable = refusal
		}
	}
	return scoped, nil
}

// ResumableRecord is ResumableIn for a holder record the caller has already
// read -- the record the TUI replays: its CanonicalInterrupted tasks, each
// carrying the repository-wide TaskRefusal of its task when one exists. The
// record's raw fold is never consulted, and refused tasks stay visible, typed.
// The caller establishes the record through ResumableIn first, so a record
// this inventory does not hold or a quarantine excludes is refused there.
func (d Discovery) ResumableRecord(record []event.Event) []Interrupted {
	tasks := CanonicalInterrupted(record)
	for i := range tasks {
		if _, refusal := d.TaskRefusal(tasks[i].TaskID); refusal != nil {
			tasks[i].Unavailable = refusal
		}
	}
	return tasks
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
	for _, q := range d.Quarantined {
		if q.SessionID == sessionID {
			return nil, fmt.Errorf("%w: session %s is excluded from task reconstruction by its quarantine manifest; "+
				"the tasks it claims (%s) are reserved and are not resumable", ErrSessionQuarantined, sessionID,
				strings.Join(q.Manifest.Claims, ", "))
		}
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
// being confidently declared gone. claimInventory makes that policy one
// definition, shared with admission (soleTaskLedger), rather than a habit
// repeated at each call site.
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
// to an account describing different work. rootClaims reads the creations
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
//
// A RECORD THAT CANNOT BE READ is excluded only by a valid quarantine manifest
// (claimInventory). It is then reported in Quarantined, never in Active, and
// every identity it claims counts as created there: FindActive and admission
// (soleTaskLedger) read the same inventory, so they agree on those claims.
func FindActive(repo string) (Discovery, error) {
	// No caller context reaches discovery; the locked re-read of a damaged
	// record is bounded by recordLockWait alone.
	records, err := claimInventory(context.Background(), filepath.Join(repo, ".sensei-code", "sessions"), "")
	if err != nil {
		return Discovery{}, err
	}
	var found Discovery
	// Where each task identity was CREATED, which is the only claim to owning
	// that task's history. It is filled from every record, whatever lifecycle
	// state the task reached there.
	createdIn := map[string]string{}
	claim := func(taskID, id string) error {
		if other, ok := createdIn[taskID]; ok {
			return fmt.Errorf("task %s is recorded as created in two session records, %s and %s; "+
				"which of them is the account to continue cannot be decided from the records themselves, so "+
				"neither is chosen", taskID, other, id)
		}
		createdIn[taskID] = id
		return nil
	}
	for _, r := range records {
		id := r.sessionID
		found.Records = append(found.Records, id)
		// A record that creates one identity twice never reaches here: the
		// inventory refuses it (rootClaims, ErrDuplicateTaskRoot) for every
		// reader alike.
		for _, taskID := range r.claims {
			if err := claim(taskID, id); err != nil {
				return Discovery{}, err
			}
		}
		if r.quarantine != nil {
			found.Quarantined = append(found.Quarantined, QuarantinedRecord{SessionID: id, Manifest: *r.quarantine})
			continue
		}
		// The canonical projection: a task's operative state is decided from
		// its lineage members alone, and a task whose history cannot be
		// projected is carried with its typed refusal, never dropped.
		for _, task := range CanonicalInterrupted(r.events) {
			found.Active = append(found.Active, Active{SessionID: id, Task: task})
		}
	}
	return found, nil
}

// rootClaims is THE ONE EXTRACTION of the task identities a record's events
// claim, read by the inventory that admission and discovery share
// (claimInventory): the task identity of each TaskCreated root, sorted and
// distinct.
//
// Terminal tasks included, deliberately: this answers "which tasks does this
// record claim to have begun", and a task that has since ended was still begun
// here.
//
// Identities are taken EXACTLY as written -- never trimmed, folded or
// otherwise normalized -- because admission compares them exactly: an identity
// discovery read one way and admission another would be reserved under one
// spelling and free under the other. A creation whose identity is not
// canonical -- an empty or absent task id included -- is refused
// (ErrNoncanonicalTaskID) rather than read as claiming nothing, or as
// claiming some other spelling: what it claims is unknown, and an unknown
// claim fails closed.
//
// A record that creates one identity twice is refused (ErrDuplicateTaskRoot)
// rather than reconciled, here, so admission, discovery, selection and
// quarantine all refuse it alike. task.created is written once per task, so
// a second one means either two different pieces of work were given one name
// or a record was concatenated from two histories; FindInterrupted would fold
// both into a single reconstruction whose obligations are a mixture of the
// two, and there is no rule that recovers which events belong to which.
func rootClaims(events []event.Event) (claims []string, err error) {
	seen := map[string]bool{}
	for _, e := range events {
		if e.Kind != event.TaskCreated {
			continue
		}
		if err := canonicalTaskID(e.TaskID); err != nil {
			return nil, fmt.Errorf("a task.created root names a task identity that is not canonical: %w", err)
		}
		if seen[e.TaskID] {
			return nil, fmt.Errorf("%w: the record states the creation of task %s more than once, so the events it "+
				"holds cannot be attributed to one task; no account of that task is offered", ErrDuplicateTaskRoot, e.TaskID)
		}
		seen[e.TaskID] = true
		claims = append(claims, e.TaskID)
	}
	sort.Strings(claims)
	return claims, nil
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
		} else {
			r.Malformed = checkpointBindingFault(r.Binding)
		}
		out = append(out, r)
	}
	return out
}

// checkpointBindingFault says why b is not a durable checkpoint state, and is
// empty when it is one.
func checkpointBindingFault(b CheckpointBinding) string {
	if !b.Status.Valid() || (b.Status == CheckpointRetired) != (b.Retirement != "") ||
		(b.Retirement != "" && !b.Retirement.Valid()) {
		return fmt.Sprintf("status %q with retirement %q is not a durable checkpoint state", b.Status, b.Retirement)
	}
	return ""
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
	s.checkpointMu.Lock()
	defer s.checkpointMu.Unlock()
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
//
// Its wait for the record lock is the caller's ctx, exactly as Append's.
func (s *Store) AppendDurable(ctx context.Context, lease *TaskLease, e event.Event) error {
	return s.appendAuthorized(ctx, lease, true, e)
}

// THE RECORD LOCK (70B2a1). Every append path -- Append, AppendDurable and
// BindSessionLineage -- validates and writes only while
// holding the record's OS lock, so no writer, through another Store in this
// process or in any other process, can append between a validation and its
// write. The lock is held for one append only, the operating system releases
// it when its holder dies, and acquiring it is BOUNDED and CANCELLABLE: a
// holder that never releases cannot wedge every other writer, whose append is
// refused, typed, after recordLockWait or when its context ends, with nothing
// written. No durable state ever says "locked".

// recordLockWait is how long an append waits for the record lock before it is
// refused, and recordLockPoll how often it retries within that bound.
var (
	recordLockWait = 30 * time.Second
	recordLockPoll = 5 * time.Millisecond
)

var (
	// ErrNoStore is lineage authority asked of no Store at all.
	ErrNoStore = errors.New("no session store is open, so no task root can be proven")
	// ErrNoTaskRoot is a record that holds no TaskCreated root of the task:
	// it establishes no session lineage.
	ErrNoTaskRoot = errors.New("the session record holds no root of the task")
	// ErrSessionlessRoot is a task whose TaskCreated root names no session; it
	// is always reported wrapped in ErrSessionLineage.
	ErrSessionlessRoot = errors.New("the task's root names no session")
	// ErrMalformedTaskRoot is a task whose first TaskCreated record lacks the
	// ordinary framing every session event carries -- an event ID, a
	// timestamp, a creation source -- so it proves no holder; it is always
	// reported wrapped in ErrSessionLineage or ErrSessionAppendRefused.
	ErrMalformedTaskRoot = errors.New("the task's root is not a well-framed TaskCreated record")
	// ErrMalformedEvent is a task's session event without the ordinary
	// framing every session event carries (validFraming). Nothing it claims
	// is authority.
	ErrMalformedEvent = errors.New("the session event is not well framed")
	// ErrSessionLineage is a task whose session lineage cannot be established,
	// or a transition that does not continue it.
	ErrSessionLineage = errors.New("the task's session lineage cannot be established")
	// ErrSessionAppendRefused is a generic append whose writer the task's
	// session lineage does not authorize. Nothing was written.
	ErrSessionAppendRefused = errors.New("the task's session lineage does not authorize this append")
	// ErrSessionEventTooLarge is an append, generic or a lineage binding,
	// whose encoded event is over the ceiling Load reads. Nothing was written.
	ErrSessionEventTooLarge = errors.New("the session event is too large to be read back")
	// ErrRecordLockTimeout is an append refused because another holder kept
	// the record lock past recordLockWait. Nothing was written.
	ErrRecordLockTimeout = errors.New("the session record lock was held by another writer past the bounded wait")
	// ErrRecordLockCanceled is an append whose context ended while it waited
	// for the record lock. Nothing was written.
	ErrRecordLockCanceled = errors.New("the wait for the session record lock was cancelled")
	// ErrSessionLineageIndeterminate is a lineage transition whose write
	// could neither be completed and verified nor undone, and whose record
	// does not read back as binding it: the one binding failure that does not
	// leave the record as it was. What the record holds is then unknown.
	ErrSessionLineageIndeterminate = errors.New("the lineage transition could neither be committed nor undone")
)

// lineageCommitFault is consulted at each step of a lineage transition's
// commit (BindSessionLineage): "write", once its bytes are written; "sync",
// once they are synced; "readback", before the committed record is verified;
// and "rollback", before an uncommitted transition is undone. A non-nil
// error fails that step. It is a variable only so a test can fail each step;
// production never replaces it.
var lineageCommitFault = func(step string) error { return nil }

func isNoTaskRoot(err error) bool { return errors.Is(err, ErrNoTaskRoot) }

func isSessionLineage(err error) bool { return errors.Is(err, ErrSessionLineage) }

// lockRecord takes the record lock: first this Store's own serialization
// (gate), then the record's OS lock (lockRecordFile), under ONE wait that is
// bounded by recordLockWait and ends with ctx. No step of it waits
// unconditionally: an operation queued behind another on the same Store is
// refused, typed, when its caller cancels (ErrRecordLockCanceled) or the bound
// elapses (ErrRecordLockTimeout), exactly as one queued behind another process
// is. A ctx that has ended takes no lock, and nothing is validated or written
// after a refusal. The release frees both, OS lock first.
func (s *Store) lockRecord(ctx context.Context) (func(), error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w (%s): no caller context bounds the wait; nothing was appended", ErrRecordLockCanceled, s.path)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w (%s); nothing was appended: %w", ErrRecordLockCanceled, s.path, err)
	}
	s.gateOnce.Do(func() { s.gate = make(chan struct{}, 1) })
	deadline := time.Now().Add(recordLockWait)
	bound := time.NewTimer(recordLockWait)
	select {
	case s.gate <- struct{}{}:
		bound.Stop()
	case <-ctx.Done():
		bound.Stop()
		return nil, fmt.Errorf("%w (%s, waiting for this Store's own writer); nothing was appended: %w", ErrRecordLockCanceled, s.path, ctx.Err())
	case <-bound.C:
		return nil, fmt.Errorf("%w (%s, waited %s for this Store's own writer); nothing was appended", ErrRecordLockTimeout, s.path, recordLockWait)
	}
	release, err := lockRecordFile(ctx, s.path, time.Until(deadline))
	if err != nil {
		<-s.gate
		return nil, err
	}
	return func() {
		release()
		<-s.gate
	}, nil
}

// lockRecordFile takes the OS lock of the record at path, retrying for at
// most wait or until ctx ends. A ctx that has ended takes no lock: it is
// refused before the first attempt, and a lock taken while it ended is
// released and refused, so no validation or write follows a cancellation.
//
// The lock is held on the record's own lock file (recordLockPath), a stable
// target that exists whether or not the record does. So an ABSENT record is
// examined under exactly the serialization every binding and append holds:
// no root can be appended between a reader's finding the record absent and
// what it concludes from that, and taking the lock never creates the record
// or any task history.
func lockRecordFile(ctx context.Context, path string, wait time.Duration) (func(), error) {
	lock := recordLockPath(path)
	if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
		return nil, err
	}
	return lockFileWithin(ctx, lock, wait)
}

// recordLockPath is the lock file of the record at path: in a locks
// directory beside it, named for it. It holds no events and is never read.
func recordLockPath(path string) string {
	return filepath.Join(filepath.Dir(path), "locks", filepath.Base(path)+".lock")
}

// lockFileWithin is lockRecordFile's bounded, cancellable acquisition of the
// OS lock on the file at path, waiting at most wait.
func lockFileWithin(ctx context.Context, path string, wait time.Duration) (func(), error) {
	canceled := func(err error) error {
		return fmt.Errorf("%w (%s); nothing was appended: %w", ErrRecordLockCanceled, path, err)
	}
	if ctx == nil {
		return nil, fmt.Errorf("%w (%s): no caller context bounds the wait; nothing was appended", ErrRecordLockCanceled, path)
	}
	deadline := time.Now().Add(wait)
	for {
		if err := ctx.Err(); err != nil {
			return nil, canceled(err)
		}
		release, held, err := tryLockRecordFile(path)
		if err != nil {
			return nil, err
		}
		if held {
			if err := ctx.Err(); err != nil {
				release()
				return nil, canceled(err)
			}
			return release, nil
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("%w (%s, waited %s); nothing was appended", ErrRecordLockTimeout, path, wait)
		}
		wait := time.NewTimer(recordLockPoll)
		select {
		case <-ctx.Done():
			wait.Stop()
		case <-wait.C:
		}
	}
}

// TaskSessionLineage is taskID's canonical session lineage in this Store's
// record, its HolderSessionID proven from the record's TaskCreated root. No
// Store, and a Store whose record holds no root of the task, fail closed. It
// is TaskSessionLineageContext under no caller context: its wait for the
// record lock is bounded by recordLockWait alone.
func (s *Store) TaskSessionLineage(taskID string) (SessionLineage, error) {
	return s.TaskSessionLineageContext(context.Background(), taskID)
}

// TaskSessionLineageContext is taskID's canonical session lineage, read under
// the record lock every binding and append holds (lockRecord), so it never
// observes a lineage transition between its write, its sync, its verification
// and its undo: what it returns is a lineage the record held with no binding
// in progress. Its wait for the lock ends with ctx and after recordLockWait.
// An absent record is found absent under that same lock, and holds no root.
func (s *Store) TaskSessionLineageContext(ctx context.Context, taskID string) (SessionLineage, error) {
	if s == nil {
		return SessionLineage{}, fmt.Errorf("%w: task %s", ErrNoStore, taskID)
	}
	release, err := s.lockRecord(ctx)
	if err != nil {
		return SessionLineage{}, err
	}
	defer release()
	record, err := s.lockedRecord()
	if err != nil {
		return SessionLineage{}, err
	}
	return TaskSessionLineage(record, taskID)
}

// ErrTaskInvocationLeased refuses an invocation of a task while another
// invocation of it -- through any Store of the same record, in this process
// or any other -- holds the task's invocation lease (AcquireTaskInvocation).
// Nothing was recorded.
var ErrTaskInvocationLeased = errors.New("another invocation of this task holds its invocation lease")

// taskLeaseWait bounds how long an invocation waits for its task's lease
// before it is refused: long enough for an invocation that is ending to
// release it, and never as long as a live one may run.
var taskLeaseWait = 2 * time.Second

// ErrTaskLedgerAmbiguous is a task that more than one physical session record
// claims: a record other than this Store's holds a TaskCreated root of it. No
// winner is chosen among them, so no invocation of the task is leased and no
// lineage of it is established. It is always reported wrapped in
// ErrSessionLineage.
var ErrTaskLedgerAmbiguous = errors.New("more than one session record claims the task")

// AcquireTaskInvocation takes taskID's INVOCATION LEASE: the one OS lock,
// keyed by the task and held at the repository's level -- beside every
// session record, not beside this Store's (taskLeasePath) -- that at most one
// Run or Resume of the task holds for its whole lifetime, so a second
// invocation of the task -- through another Store, another record, Engine or
// process -- is refused before it binds a lineage or records anything. The
// lease is the kernel's, held by an open descriptor: Release ends it, and so
// does the death of the holding process, however it dies, so no crash leaves
// the task permanently unresumable. Acquisition is bounded (taskLeaseWait) and
// ends with ctx; a lease another holder keeps is ErrTaskInvocationLeased.
//
// Under the lease, this Store's record must be the task's only possible
// ledger: a task another session record claims (ErrTaskLedgerAmbiguous) is
// refused and the lease released, so two physical ledgers never carry two
// operative invocations of one task.
//
// The lease is returned as a CAPABILITY (TaskLease): the lineage-binding
// operation (BindSessionLineage) requires the live lease of the task, taken
// through a Store of the same physical record, so no binder that does not hold
// the task's lease -- another Store of the record, another Engine, a caller
// that skipped the lease -- can move the task's lineage tip.
func (s *Store) AcquireTaskInvocation(ctx context.Context, taskID string) (*TaskLease, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: task %s", ErrNoStore, taskID)
	}
	if err := canonicalTaskID(taskID); err != nil {
		return nil, fmt.Errorf("an invocation of task %q is refused: %w", taskID, err)
	}
	lease := s.taskLeasePath(taskID)
	if err := os.MkdirAll(filepath.Dir(lease), 0o755); err != nil {
		return nil, err
	}
	release, err := lockFileWithin(ctx, lease, taskLeaseWait)
	if errors.Is(err, ErrRecordLockTimeout) {
		return nil, fmt.Errorf("%w: task %s: %w", ErrTaskInvocationLeased, taskID, err)
	}
	if err != nil {
		return nil, err
	}
	if err := s.soleTaskLedger(ctx, taskID, false); err != nil {
		release()
		return nil, err
	}
	l := &TaskLease{taskID: taskID, leasePath: lease, recordPath: s.path, release: release, live: true}
	l.idle = sync.NewCond(&l.mu)
	return l, nil
}

// TaskLease is the capability of holding one task's invocation lease
// (AcquireTaskInvocation). It is opaque: only AcquireTaskInvocation makes a
// live one, so a zero or released lease authorizes nothing. It names the
// canonical task it was taken for, the lease file it holds and the physical
// record of the Store that took it; the lineage-binding operation verifies all
// three against its own Store and the transition (guardTaskLease).
type TaskLease struct {
	taskID     string
	leasePath  string
	recordPath string
	mu         sync.Mutex
	release    func()
	live       bool
	// closing is set by the first Release: from then on the lease admits no
	// new operation (begin), though the operations it already admitted run
	// to their end. ops counts those admitted operations, and idle -- on mu
	// -- is signalled when the last of them ends.
	closing bool
	ops     int
	idle    *sync.Cond
	// session is the one session the lease is operative for, set only by
	// the Store -- by the root it created (CreateTaskRoot), the transition it
	// bound (BindSessionLineage) or the tip it continued
	// (ContinueTaskSession) -- and "" until then. Generic appends under the
	// lease are authorized for this session and no other.
	session string
}

// operative is the session the lease is operative for, "" when none.
func (l *TaskLease) operative() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.session
}

// makeOperative makes the lease operative for sessionID. Only the Store's
// root creation, lineage binding and tip continuation call it, each under the
// record lock and after verifying the lease.
func (l *TaskLease) makeOperative(sessionID string) {
	l.mu.Lock()
	l.session = sessionID
	l.mu.Unlock()
}

// operativeFor refuses a write under the lease by sessionID unless the lease
// is live and operative for exactly that session.
func (l *TaskLease) operativeFor(sessionID string) error {
	if l == nil {
		return fmt.Errorf("%w: no lease is presented", ErrTaskLeaseRequired)
	}
	l.mu.Lock()
	live, op := l.live, l.session
	l.mu.Unlock()
	switch {
	case !live:
		return fmt.Errorf("%w: the lease presented is not live", ErrTaskLeaseRequired)
	case op == "":
		return fmt.Errorf("%w: the lease presented has made no session operative; session %s records nothing under it",
			ErrTaskLeaseRequired, orNoSession(sessionID))
	case op != sessionID:
		return fmt.Errorf("%w: the lease presented is operative for session %s, not %s", ErrTaskLeaseRequired, op, orNoSession(sessionID))
	}
	return nil
}

// TaskID is the task the lease was taken for.
func (l *TaskLease) TaskID() string {
	if l == nil {
		return ""
	}
	return l.taskID
}

// Release ends the lease. THE LINEARIZATION POINT of the lease's authority
// is its operation guard (begin): an operation the lease admitted before
// Release closed it runs to its end -- record lock, authorization, commit,
// read-back or undo, and the operative-session change -- under the lease, and
// Release waits for every such operation to end before the lease stops being
// live and its OS lock is freed, so no competing invocation can acquire the
// task while an operation of this one may still write. Release closes the
// lease to new operations first, so an operation not admitted by then is
// refused. Every call returns only once the lease is released; only the
// first frees the OS lock. Release waits for nothing but admitted
// operations, each of which is bounded by its own record-lock wait.
func (l *TaskLease) Release() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.closing = true
	for l.ops > 0 {
		l.idle.Wait()
	}
	release := l.release
	l.release, l.live, l.session = nil, false, ""
	l.mu.Unlock()
	if release != nil {
		release()
	}
}

// begin admits one Store operation under the lease, atomically: the lease is
// live and not closing, and the operation is counted, so Release waits for it
// (end). A lease that is released or closing admits nothing.
func (l *TaskLease) begin() (end func(), err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.live || l.closing || l.idle == nil {
		return nil, fmt.Errorf("%w: the lease presented for task %s is not live", ErrTaskLeaseRequired, l.taskID)
	}
	l.ops++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			l.ops--
			if l.ops == 0 {
				l.idle.Broadcast()
			}
			l.mu.Unlock()
		})
	}, nil
}

// ErrTaskLeaseRequired is a task-bound write -- a root, a lineage binding, an
// append or an append unit -- whose caller does not present the task's live
// invocation lease through a Store of the same physical record, operative
// (for an append) for exactly the writing session: no lease, a released one,
// one of another task, one taken through another record, or one operative for
// no session or another. Nothing was written.
var ErrTaskLeaseRequired = errors.New("the write is not made under the task's live invocation lease")

// guardTaskLease is THE OPERATION GUARD of every task-bound Store write: it
// proves that lease is the invocation lease of taskID, taken through a Store
// of this Store's physical record (leaseIdentity), and admits the operation
// under it (begin), which keeps the lease live until end is called. Its
// callers take it before the record lock and end it only after their commit,
// read-back or undo and any operative-session change, so the capability that
// authorized a write is held, unreleasable, until the write is settled. It
// waits on nothing but the lease's own field guard.
func (s *Store) guardTaskLease(lease *TaskLease, taskID string) (end func(), err error) {
	if err := s.leaseIdentity(lease, taskID); err != nil {
		return nil, err
	}
	return lease.begin()
}

// leaseIdentity proves that lease was taken for taskID through a Store of
// this Store's physical record: its immutable identity, which no Release
// changes.
func (s *Store) leaseIdentity(lease *TaskLease, taskID string) error {
	if lease == nil {
		return fmt.Errorf("%w: no lease is presented for task %s", ErrTaskLeaseRequired, taskID)
	}
	switch {
	case lease.taskID != taskID:
		return fmt.Errorf("%w: the lease presented for task %s is task %s's", ErrTaskLeaseRequired, taskID, lease.taskID)
	case lease.leasePath != s.taskLeasePath(taskID):
		return fmt.Errorf("%w: the lease presented for task %s is held at %s, not at the task's lease %s",
			ErrTaskLeaseRequired, taskID, lease.leasePath, s.taskLeasePath(taskID))
	case !sameRecord(lease.recordPath, s.path):
		return fmt.Errorf("%w: the lease presented for task %s was taken through record %s, not %s",
			ErrTaskLeaseRequired, taskID, lease.recordPath, s.path)
	}
	return nil
}

// sameRecord reports whether a and b name the same physical session record:
// the same cleaned path, or -- through another spelling or link -- the same
// file.
func sameRecord(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ai, aerr := os.Stat(a)
	bi, berr := os.Stat(b)
	return aerr == nil && berr == nil && os.SameFile(ai, bi)
}

// taskLeasePath is taskID's invocation lease file: one per task, in the
// invocations directory beside the repository's sessions directory, so every
// Store of every record of the repository contends for the same file.
func (s *Store) taskLeasePath(taskID string) string {
	sessions := filepath.Dir(filepath.Dir(s.path))
	return filepath.Join(filepath.Dir(sessions), "invocations", hex.EncodeToString([]byte(taskID))+".lease")
}

// soleTaskLedger refuses taskID when any session record of the repository
// other than this Store's holds a TaskCreated root of it
// (ErrTaskLedgerAmbiguous): a task has one physical ledger, and which of two
// is the account to continue cannot be decided from the records. A record
// that cannot be read is an error, never absence -- unless a valid quarantine
// manifest asserts its recovery (claimInventory), and then every task
// identity it claims stays reserved (ErrTaskQuarantined) while unrelated
// tasks proceed. A Store whose own record is quarantined admits no task at
// all (ErrSessionQuarantined): quarantine grants no authority to resume or
// repair the account it excludes. Its callers hold the task's invocation
// lease, and any record lock it takes is taken after it (lease before record
// lock), bounded by recordLockWait and ended by ctx.
//
// THIS STORE'S OWN RECORD IS VALIDATED, NOT SKIPPED: it is read by the same
// inventory reader (readLedgerRecord), so a torn or corrupt own record that no
// valid manifest excludes, a noncanonical own root, and a malformed, stale or
// readable-record manifest beside it fail closed with their own typed errors,
// exactly as discovery sees them, and only a manifest valid for the own
// record's exact bytes makes it ErrSessionQuarantined. Its own claims are not
// a competing ledger. locked says the caller already holds this Store's
// record lock (CreateTaskRoot, BindSessionLineage): the own record is then
// read under that lock and never locked again.
func (s *Store) soleTaskLedger(ctx context.Context, taskID string, locked bool) error {
	held := ""
	if locked {
		held = s.path
	}
	records, err := claimInventory(ctx, filepath.Dir(filepath.Dir(s.path)), held)
	if err != nil {
		return fmt.Errorf("whether another session record claims task %s is unknown: %w", taskID, err)
	}
	own := func(r ledgerRecord) bool { return filepath.Clean(r.path) == filepath.Clean(s.path) }
	for _, r := range records {
		if own(r) && r.quarantine != nil {
			return fmt.Errorf("%w: %w: session record %s is quarantined, so no task is admitted through it",
				ErrSessionLineage, ErrSessionQuarantined, s.path)
		}
	}
	for _, r := range records {
		if own(r) || !slices.Contains(r.claims, taskID) {
			continue
		}
		if r.quarantine != nil {
			return fmt.Errorf("%w: %w: task %s is claimed by quarantined session record %s; the identity is never "+
				"minted or admitted again", ErrSessionLineage, ErrTaskQuarantined, taskID, r.path)
		}
		return fmt.Errorf("%w: %w: task %s is claimed by %s as well as %s; neither is chosen",
			ErrSessionLineage, ErrTaskLedgerAmbiguous, taskID, r.path, s.path)
	}
	return nil
}

// HolderSessionID is the session that wrote taskID's TaskCreated root in this
// Store's record: proven from the durable root, never taken from a caller.
func (s *Store) HolderSessionID(taskID string) (string, error) {
	l, err := s.TaskSessionLineage(taskID)
	if err != nil {
		return "", err
	}
	return l.HolderSessionID, nil
}

// BindSessionLineage is the ONE writer of a SessionLineageBound record and
// the single choke point through which a fresh session becomes a task's
// current session. Under the record lock it rereads the record, proves the
// task's root and holder, and requires b to be exactly the transition the
// lineage owes now: the proven holder, the current tip as parent, and a
// current session the lineage does not already name. Generic appends by
// b.CurrentSessionID are authorized once it returns the lineage. Its caller
// must hold the task's live invocation lease (lease, AcquireTaskInvocation),
// taken through a Store of this same physical record; a binder that does not
// is refused (ErrTaskLeaseRequired) and writes nothing, so no competing binder
// can move the tip while an invocation of the task holds the lease.
//
// ITS COMMIT IS ALL OR NOTHING. The transition is verified as the record
// will read it back -- the record as it stands, plus the transition exactly
// as encoded -- before a byte is written. Then it is appended and synced, and
// the record is read back from disk and must establish b.CurrentSessionID as
// the tip. If any step of that fails, the transition is undone: the record
// is truncated to the length it had under the lock, synced, and so is byte
// for byte what it was, and the binding is refused. Only when the undo
// itself fails, and only for a transition whose sync completed, do the
// record's own bytes decide: a record that reads back as binding
// b.CurrentSessionID is a committed transition and is returned as bound. A
// transition written but not known to be synced, and any other record, is
// ErrSessionLineageIndeterminate: visible is not durable. So a refusal never
// follows a change to the record, and no session is reported bound by a
// transition that is not proven durable. The lock is released either way.
func (s *Store) BindSessionLineage(ctx context.Context, lease *TaskLease, b SessionLineageBinding) (SessionLineage, error) {
	if s == nil {
		return SessionLineage{}, fmt.Errorf("%w: task %s", ErrNoStore, b.TaskID)
	}
	if err := canonicalTaskID(b.TaskID); err != nil {
		return SessionLineage{}, fmt.Errorf("%w: a transition of task %q: %w", ErrSessionLineage, b.TaskID, err)
	}
	e := event.New(b.CurrentSessionID, b.TaskID, event.SourceSystem, SessionLineageBound,
		"session "+b.CurrentSessionID+" continues task "+b.TaskID+" from session "+b.ParentSessionID, b)
	// Bounded exactly as every generic append is, before the lock is taken
	// and before any byte is written: a transition Load could not read back
	// would leave the whole record unopenable.
	var buf bytes.Buffer
	line, err := encodeBounded(&buf, e)
	if err != nil {
		return SessionLineage{}, err
	}
	var staged event.Event
	if err := json.Unmarshal(line, &staged); err != nil {
		return SessionLineage{}, err
	}
	// THE BINDER HOLDS THE TASK'S LEASE, admitted before the record lock is
	// taken and held through commit, read-back and undo (guardTaskLease): a
	// binder without the live lease of this task, taken through this
	// physical record, moves nothing, and no Release overtakes a binding the
	// lease admitted.
	end, err := s.guardTaskLease(lease, b.TaskID)
	if err != nil {
		return SessionLineage{}, err
	}
	defer end()
	release, err := s.lockRecord(ctx)
	if err != nil {
		return SessionLineage{}, err
	}
	defer release()
	// One invocation, one session: a lease already operative for a session
	// binds no other.
	if op := lease.operative(); op != "" {
		return SessionLineage{}, fmt.Errorf("%w: the lease presented for task %s is already operative for session %s",
			ErrTaskLeaseRequired, b.TaskID, op)
	}
	record, err := s.lockedRecord()
	if err != nil {
		return SessionLineage{}, err
	}
	l, err := TaskSessionLineage(record, b.TaskID)
	if err != nil {
		return SessionLineage{}, err
	}
	if err := l.admits(b); err != nil {
		return SessionLineage{}, err
	}
	// The unique canonical holder ledger is proven before the transition is
	// written: a task another record also claims binds nothing.
	if err := s.soleTaskLedger(ctx, b.TaskID, true); err != nil {
		return SessionLineage{}, err
	}
	// Staged: the record this commit would leave, verified before it exists.
	projected, err := TaskSessionLineage(append(record[:len(record):len(record)], staged), b.TaskID)
	if err != nil {
		return SessionLineage{}, fmt.Errorf("the transition, as the record would read it back, breaks task %s's lineage: %w", b.TaskID, err)
	}
	if projected.Tip() != b.CurrentSessionID {
		return SessionLineage{}, fmt.Errorf("%w: the transition, as the record would read it back, makes %q, not %q, task %s's current session",
			ErrSessionLineage, projected.Tip(), b.CurrentSessionID, b.TaskID)
	}
	info, err := os.Stat(s.path)
	if err != nil {
		return SessionLineage{}, err
	}
	// The record's own name is made durable before anything is appended to
	// it, so a refusal here has written nothing.
	if err := syncDir(filepath.Dir(s.path)); err != nil {
		return SessionLineage{}, err
	}
	s.cache = nil
	bound, err := s.commitBinding(info.Size(), line, b)
	if err != nil {
		return SessionLineage{}, err
	}
	// The committed transition is what makes the lease operative for
	// b.CurrentSessionID; nothing before it does.
	lease.makeOperative(b.CurrentSessionID)
	return bound, nil
}

// commitBinding appends, syncs and verifies the staged transition line, the
// record having held size bytes, and undoes it on any failure (undoBinding).
// Its callers hold the record lock.
func (s *Store) commitBinding(size int64, line []byte, b SessionLineageBinding) (SessionLineage, error) {
	if synced, err := s.commitAppend(line); err != nil {
		return s.undoBinding(size, b, err, synced)
	}
	if err := lineageCommitFault("readback"); err != nil {
		return s.undoBinding(size, b, err, true)
	}
	bound, err := s.boundAsRead(b)
	if err != nil {
		return s.undoBinding(size, b, err, true)
	}
	return bound, nil
}

// commitAppend appends line to the record and syncs it. synced reports that
// the sync of the appended bytes completed: only then is the transition
// durable, whatever a later read of the record shows. Its callers hold the
// record lock.
func (s *Store) commitAppend(line []byte) (synced bool, err error) {
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return false, err
	}
	if _, err := f.Write(line); err != nil {
		f.Close()
		return false, err
	}
	if err := lineageCommitFault("write"); err != nil {
		f.Close()
		return false, err
	}
	// The injected "sync" failure stands in for the sync itself, so a test
	// fails exactly what a real fsync failure fails: the bytes are written
	// and readable, and not known to be durable.
	if err := lineageCommitFault("sync"); err != nil {
		f.Close()
		return false, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return false, err
	}
	return true, f.Close()
}

// boundAsRead is task b.TaskID's lineage as the record reads back from disk
// now, required to name b.CurrentSessionID as its tip. Its callers hold the
// record lock.
func (s *Store) boundAsRead(b SessionLineageBinding) (SessionLineage, error) {
	s.cache = nil
	readback, err := s.lockedRecord()
	if err != nil {
		return SessionLineage{}, err
	}
	bound, err := TaskSessionLineage(readback, b.TaskID)
	if err != nil {
		return SessionLineage{}, fmt.Errorf("the record read back after binding session %q does not establish task %s's lineage: %w",
			b.CurrentSessionID, b.TaskID, err)
	}
	if bound.Tip() != b.CurrentSessionID {
		return SessionLineage{}, fmt.Errorf("%w: the record read back establishes %q, not %q, as task %s's current session",
			ErrSessionLineage, bound.Tip(), b.CurrentSessionID, b.TaskID)
	}
	return bound, nil
}

// undoBinding undoes a lineage transition whose commit failed with cause: the
// record is truncated back to size, the length it had under the lock before
// the transition was appended, and synced. The record is then byte for byte
// what it was, and the binding is refused with cause. When it cannot be
// undone, a transition whose sync completed (synced) is durable, and the
// record's own bytes decide: one that reads back as binding
// b.CurrentSessionID holds a committed transition, which is returned bound.
// Any other -- above all a transition written but never known to be synced,
// which a read can show and a crash can still lose -- is
// ErrSessionLineageIndeterminate, and makes no session operative. Its callers
// hold the record lock.
func (s *Store) undoBinding(size int64, b SessionLineageBinding, cause error, synced bool) (SessionLineage, error) {
	s.cache = nil
	undo := lineageCommitFault("rollback")
	if undo == nil {
		undo = truncateRecord(s.path, size)
	}
	if undo == nil {
		return SessionLineage{}, fmt.Errorf("the lineage transition binding session %q to task %s was not committed, "+
			"and the record is restored to the %d bytes it held: %w", b.CurrentSessionID, b.TaskID, size, cause)
	}
	if synced {
		if bound, err := s.boundAsRead(b); err == nil {
			return bound, nil
		}
	}
	return SessionLineage{}, fmt.Errorf("%w: binding session %q to task %s failed (%v), and the record could not be restored to its %d bytes (%v)",
		ErrSessionLineageIndeterminate, b.CurrentSessionID, b.TaskID, cause, size, undo)
}

// truncateRecord truncates the record at path to size and syncs it, and
// confirms the record then has exactly that length.
func truncateRecord(path string, size int64) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if err := f.Truncate(size); err != nil {
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
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() != size {
		return fmt.Errorf("the record is %d bytes after truncation to %d", info.Size(), size)
	}
	return nil
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

// THE DURABLE QUARANTINE (70B2a1R, DF-41 D5; RULING-223/224, Design B).
//
// One unreadable historical session record used to deny task creation and
// discovery for every task in the repository: admission (soleTaskLedger) and
// discovery (FindActive) refuse a record they cannot read, because the task
// being asked about may be inside it. Fail-closed is right; its scope was not.
//
// A damaged record is excluded from task-state reconstruction ONLY by an
// authenticated, durable quarantine assertion -- a manifest beside it,
// sessions/<id>/quarantine.json -- that preserves its bytes and keeps every
// task identity it claims, or may claim, permanently reserved. The record is
// never moved, truncated, rewritten or replaced by synthesized events. The
// manifest is a recovery assertion, not an event ledger and not a second task
// registry: it is valid only while it matches the record's current bytes
// exactly -- size, SHA-256, and a classification recomputed from those bytes
// -- and a missing, malformed, conflicting, stale or orphan one fails closed,
// exactly as the unreadable record did. A manifest beside a record that reads
// is never a recovery. Nothing here deletes or modifies historical events, and
// nothing here runs on its own: a manifest is written only by
// ActivateQuarantine, at the explicit request of the human owner, who names
// the exact size and digest they authorized.

var (
	// ErrNoncanonicalTaskID is a task identity refused at the admission
	// boundary because it is not canonical: empty, with surrounding space,
	// not valid UTF-8, or holding a control character. Nothing was recorded.
	ErrNoncanonicalTaskID = errors.New("the task identity is not canonical")
	// ErrTaskQuarantined is a task identity a quarantined session record
	// claims: it is reserved, and never minted or admitted again. It is
	// always reported wrapped in ErrSessionLineage.
	ErrTaskQuarantined = errors.New("the task identity is reserved by a quarantined session record")
	// ErrSessionQuarantined is a session record excluded from task
	// reconstruction by its quarantine manifest: nothing of it is resumed,
	// and no task is admitted through it.
	ErrSessionQuarantined = errors.New("the session record is quarantined")
	// ErrRecordUnreadable is a session record that could not be read and that
	// no valid quarantine manifest excludes: the tasks it holds are unknown
	// rather than absent.
	ErrRecordUnreadable = errors.New("the session record could not be read")
	// ErrQuarantineInvalid is a quarantine manifest that is not a valid
	// recovery assertion for the record beside it: malformed, of another
	// record, stale, orphaned, under- or over-claiming, or beside a record
	// that reads. It excludes nothing; the record fails closed.
	ErrQuarantineInvalid = errors.New("the quarantine manifest is not a valid recovery assertion")
	// ErrQuarantineRefused is an activation refused before any manifest was
	// published: the record is not a torn record whose every claim is known,
	// the authorization is incomplete, or a manifest already exists.
	ErrQuarantineRefused = errors.New("the session record cannot be quarantined")
	// ErrQuarantineMismatch is an activation whose record is not the exact
	// bytes -- size and SHA-256 -- the human owner authorized. Nothing was
	// published.
	ErrQuarantineMismatch = errors.New("the session record is not the bytes the owner authorized")
	// ErrQuarantineIndeterminate is an activation whose manifest was
	// published but whose durability or read-back could not be confirmed.
	// Whatever it left is either a valid manifest, whose claims are reserved,
	// or none or an invalid one, which fails closed: no outcome releases a
	// claim.
	ErrQuarantineIndeterminate = errors.New("the quarantine manifest was published but not confirmed")
	// ErrQuarantineSync is a real Sync of the manifest's file or of its
	// directory that failed, so its durability is not established. It is
	// always reported wrapped in ErrQuarantineRefused (the file's, before
	// publication) or ErrQuarantineIndeterminate (the directory's, after it),
	// and never as a successful activation.
	ErrQuarantineSync = errors.New("the quarantine manifest could not be synchronized")
	// ErrDuplicateTaskRoot is a session record whose events create one task
	// identity more than once (rootClaims). Its events cannot be attributed
	// to one task, so every reader of the inventory refuses it alike and it
	// is never quarantinable.
	ErrDuplicateTaskRoot = errors.New("the session record creates one task identity more than once")
)

// canonicalTaskID refuses a task identity that is not canonical
// (ErrNoncanonicalTaskID). Identities are compared exactly; one that is
// already canonical is unchanged, and none is migrated.
func canonicalTaskID(id string) error {
	if reason := noncanonical(id); reason != "" {
		return fmt.Errorf("%w: %q %s", ErrNoncanonicalTaskID, id, reason)
	}
	return nil
}

// noncanonical says why s is not a canonical identity or text, "" when it
// is: not empty, no surrounding space, valid UTF-8, no control character.
func noncanonical(s string) string {
	switch {
	case s == "":
		return "is empty"
	case !utf8.ValidString(s):
		return "is not valid UTF-8"
	case strings.TrimSpace(s) != s:
		return "has surrounding space"
	case strings.IndexFunc(s, unicode.IsControl) >= 0:
		return "holds a control character"
	}
	return ""
}

// RecordClass is what a session record's exact bytes are, decided from the
// bytes alone (classifyRecord).
type RecordClass string

const (
	// RecordReadable is a record every line of which is terminated and
	// decodes: the record Load reads.
	RecordReadable RecordClass = "readable"
	// RecordTorn is a record whose terminated lines all decode and whose
	// unterminated final segment does not: an append interrupted mid-write.
	RecordTorn RecordClass = "torn"
	// RecordAmbiguous is a record whose unterminated final segment decodes:
	// whether that event was completed cannot be told. It fails closed.
	RecordAmbiguous RecordClass = "ambiguous"
	// RecordCorrupt is a record holding a terminated line that does not
	// decode, or a line over the bound an event may occupy. It fails closed.
	RecordCorrupt RecordClass = "corrupt"
)

// QuarantineTail is the bounded classification of a torn record's
// unterminated final segment: its length, the event kind its top-level
// members legibly name, and the task identity they legibly name, if any.
type QuarantineTail struct {
	Size   int64  `json:"size"`
	Kind   string `json:"kind"`
	TaskID string `json:"task_id,omitempty"`
}

// recordScan is one record's exact bytes, classified.
type recordScan struct {
	class  RecordClass
	size   int64
	sha256 string
	// events are the complete prefix's events, each decoded exactly as Load
	// decodes it; prefixEnd is the byte at which that prefix ends.
	events    []event.Event
	prefixEnd int64
	// claims is every task identity the record claims or may claim, sorted
	// and distinct: each TaskCreated root of the prefix, and a torn tail's
	// legible task identity, reserved conservatively.
	claims []string
	// claimErr is a prefix root whose identity is not canonical, or one
	// created more than once (rootClaims).
	claimErr error
	tail     QuarantineTail
	// refusal says why the record cannot be quarantined; "" only for a torn
	// record whose every claim is known.
	refusal string
}

// quarantinable reports whether the record may be quarantined: torn, its
// every prefix event classified and every claim collected, its tail bounded,
// understood and hiding no further claim.
func (sc recordScan) quarantinable() bool { return sc.class == RecordTorn && sc.refusal == "" }

// classifyRecord classifies a record's exact bytes. Every terminated line is
// decoded as Load decodes it and bounded as Load bounds it; the remainder, if
// any, is the unterminated final segment. A segment that decodes is
// ambiguous; one that does not is torn, and is quarantinable only when every
// prefix line and the segment itself are structurally understood
// (walkEventIdentity) and every claim is canonical.
func classifyRecord(raw []byte) recordScan {
	sum := sha256.Sum256(raw)
	sc := recordScan{size: int64(len(raw)), sha256: hex.EncodeToString(sum[:]), claims: []string{}}
	off := 0
	var lines [][]byte
	// rawErr is the first TaskCreated root whose task identity the decoder
	// would read under an identity its bytes do not hold (losslessTaskID).
	var rawErr error
	for {
		nl := bytes.IndexByte(raw[off:], '\n')
		if nl < 0 {
			break
		}
		line := raw[off : off+nl]
		if len(line) > maxSessionEvent {
			sc.class, sc.prefixEnd = RecordCorrupt, int64(off)
			sc.refusal = fmt.Sprintf("the line at byte %d is %d bytes, over the %d-byte maximum a single event may occupy",
				off, len(line), maxSessionEvent)
			return sc
		}
		var e event.Event
		if err := json.Unmarshal(line, &e); err != nil {
			sc.class, sc.prefixEnd = RecordCorrupt, int64(off)
			sc.refusal = fmt.Sprintf("the terminated line at byte %d does not decode as a session event, so it is corrupt "+
				"rather than torn: %v", off, err)
			return sc
		}
		if e.Kind == event.TaskCreated && rawErr == nil {
			rawErr = losslessTaskID(line)
		}
		sc.events = append(sc.events, e)
		lines = append(lines, line)
		off += nl + 1
	}
	sc.prefixEnd = int64(off)
	prefixClaims, claimErr := rootClaims(sc.events)
	if claimErr == nil {
		claimErr = rawErr
	}
	sc.claimErr = claimErr
	claims := map[string]bool{}
	for _, id := range prefixClaims {
		claims[id] = true
	}
	tail := raw[off:]
	var e event.Event
	switch {
	case len(tail) == 0:
		sc.class, sc.refusal = RecordReadable, "the record is readable, so nothing about it is recovered by a quarantine"
	case json.Unmarshal(tail, &e) == nil:
		sc.class = RecordAmbiguous
		sc.refusal = fmt.Sprintf("the unterminated final segment at byte %d decodes, so whether that event was completed is "+
			"ambiguous", off)
	default:
		sc.class = RecordTorn
		sc.tail.Size = int64(len(tail))
		if sc.refusal = prefixRefusal(lines, sc.events, claimErr); sc.refusal != "" {
			break
		}
		if len(tail) > maxSessionEvent {
			sc.refusal = fmt.Sprintf("the unterminated final segment at byte %d is %d bytes, over the %d-byte bound a torn "+
				"event is inspected within", off, len(tail), maxSessionEvent)
			break
		}
		kind, taskID, err := walkEventIdentity(tail, false)
		if err == nil && taskID != "" {
			err = canonicalTaskID(taskID)
		}
		if err != nil {
			sc.refusal = fmt.Sprintf("the unterminated final segment at byte %d is not understood: %v", off, err)
			break
		}
		sc.tail.Kind, sc.tail.TaskID = kind, taskID
		if taskID != "" {
			claims[taskID] = true
		}
	}
	for id := range claims {
		sc.claims = append(sc.claims, id)
	}
	sort.Strings(sc.claims)
	return sc
}

// prefixRefusal proves every complete prefix line of a torn record was
// classified and every claim it makes collected, or says why not: each line
// is walked structurally under the closed event vocabulary
// (walkEventIdentity), and must name exactly the kind and task identity it
// decoded as, so no member the decoder folded, overwrote or unescaped hides
// a TaskCreated root; and every root's identity must be canonical. Only a
// record that is not readable is walked: a readable record is the account
// Load reads, and quarantine never applies to it.
func prefixRefusal(lines [][]byte, events []event.Event, claimErr error) string {
	for i, line := range lines {
		kind, taskID, err := walkEventIdentity(line, true)
		if err == nil && (kind != string(events[i].Kind) || taskID != events[i].TaskID) {
			err = fmt.Errorf("it names kind %q and task %q, and decodes as kind %q and task %q", kind, taskID,
				events[i].Kind, events[i].TaskID)
		}
		if err != nil {
			return fmt.Sprintf("complete prefix event %d is not classified, so the claims it makes are unknown: %v", i+1, err)
		}
	}
	if claimErr != nil {
		return claimErr.Error()
	}
	return ""
}

// walkEventIdentity inspects one event's bytes STRUCTURALLY -- by its JSON
// tokens, never by a substring test -- as the decoder reads them: one object
// whose top-level members it matches with its own case-insensitive key
// matching (strings.EqualFold is bytes.EqualFold, which encoding/json folds
// keys by). complete says whether raw is a terminated line, which must be one
// whole object, or a torn segment, which must be one object truncated before
// it closes. It returns the event kind and the task identity raw legibly
// names, and refuses -- an unknown claim fails closed -- bytes that are not
// that one object; a kind or task identity member that is repeated, spelled
// in any way but exactly as the event encodes it (another case, or escaped),
// not a string, or truncated; a kind that is absent or not a registered kind;
// a kind or task identity whose bytes the decoder would replace (lossless);
// and in a torn segment, a TaskCreated whose task identity is not legible, or
// a top-level member name cut before it can be proven distinct from kind and
// task_id (mayNameIdentity): a claim that could hide there.
func walkEventIdentity(raw []byte, complete bool) (kind, taskID string, err error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return "", "", errors.New("it does not begin one JSON object")
	}
	tracked := func(key string) bool { return strings.EqualFold(key, "kind") || strings.EqualFold(key, "task_id") }
	var kinds, ids []string
	pending, depth, expectKey, closed := "", 1, true, false
	for !closed {
		before := dec.InputOffset()
		tok, err := dec.Token()
		if err != nil {
			if complete || (!errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF)) {
				return "", "", fmt.Errorf("it is malformed, not truncated: %v", err)
			}
			if depth == 1 && expectKey && mayNameIdentity(raw[before:]) {
				return "", "", errors.New("it is truncated inside a top-level member name that cannot be proven " +
					"distinct from kind or task_id, so a further kind or task identity may hide there")
			}
			break
		}
		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '{', '[':
				if depth == 1 && tracked(pending) {
					return "", "", fmt.Errorf("its member %q is not a string", pending)
				}
				depth++
			default:
				depth--
				if depth == 0 {
					if !complete {
						return "", "", errors.New("it closes its object: a complete value that does not decode is not torn")
					}
					closed = true
				}
				if depth == 1 {
					pending, expectKey = "", true
				}
			}
			continue
		}
		if depth != 1 {
			continue
		}
		if expectKey {
			pending, _ = tok.(string)
			if tracked(pending) && (pending != "kind" && pending != "task_id" ||
				bytes.IndexByte(raw[before:dec.InputOffset()], '\\') >= 0) {
				return "", "", fmt.Errorf("its member %q is not spelled exactly as an event encodes it, so which "+
					"member the decoder reads as it is not legible", pending)
			}
			expectKey = false
			continue
		}
		s, isString := tok.(string)
		if tracked(pending) && !isString {
			return "", "", fmt.Errorf("its member %q is not a string", pending)
		}
		if tracked(pending) && !lossless(raw[before:dec.InputOffset()]) {
			return "", "", fmt.Errorf("%w: its member %q is not written byte-faithfully (invalid UTF-8 or an unpaired "+
				"surrogate escape), so the decoder would read it as an identity its bytes do not hold",
				ErrNoncanonicalTaskID, pending)
		}
		switch pending {
		case "kind":
			kinds = append(kinds, s)
		case "task_id":
			ids = append(ids, s)
		}
		pending, expectKey = "", true
	}
	if !complete && depth == 1 && !expectKey && tracked(pending) {
		return "", "", fmt.Errorf("its member %q is truncated, so what it names is illegible", pending)
	}
	switch {
	case len(kinds) == 0:
		return "", "", errors.New("it names no legible kind, and an unknown kind is never harmless")
	case len(kinds) > 1:
		return "", "", errors.New("it names its kind more than once")
	case len(ids) > 1:
		return "", "", errors.New("it names its task more than once")
	}
	if _, ok := governedKinds[event.Kind(kinds[0])]; !ok {
		return "", "", fmt.Errorf("its kind %q is not a registered session event kind", kinds[0])
	}
	if len(ids) == 1 {
		taskID = ids[0]
	}
	if !complete && event.Kind(kinds[0]) == event.TaskCreated && taskID == "" {
		return "", "", errors.New("it is a TaskCreated root whose task identity is not legible, so its claim is unknown")
	}
	return kinds[0], taskID, nil
}

// mayNameIdentity reports whether rest -- the bytes after the last complete
// top-level member of a torn segment -- begins a member name that could still
// be kind or task_id: one that is escaped, not valid UTF-8, or a prefix of
// either under the decoder's case folding. Only a name proven distinct from
// both, or no name at all, hides no identity.
func mayNameIdentity(rest []byte) bool {
	rest = bytes.TrimLeft(rest, " \t\r\n,")
	if len(rest) == 0 {
		return false
	}
	if rest[0] != '"' {
		return true
	}
	name := rest[1:]
	if bytes.ContainsAny(name, `\"`) || !utf8.Valid(name) {
		return true
	}
	return foldPrefix(string(name), "kind") || foldPrefix(string(name), "task_id")
}

// foldPrefix reports whether p is a prefix of target, rune by rune, under the
// case folding the decoder matches member names with.
func foldPrefix(p, target string) bool {
	ps, ts := []rune(p), []rune(target)
	if len(ps) > len(ts) {
		return false
	}
	for i, r := range ps {
		if !strings.EqualFold(string(r), string(ts[i])) {
			return false
		}
	}
	return true
}

// losslessTaskID refuses (ErrNoncanonicalTaskID) a terminated line one of
// whose top-level task identity members, under any spelling the decoder
// matches, is not written byte-faithfully: encoding/json replaces invalid
// UTF-8 and unpaired surrogate escapes with U+FFFD, so the identity it would
// decode is not the one the record holds, and exact comparison is lost.
func losslessTaskID(line []byte) error {
	dec := json.NewDecoder(bytes.NewReader(line))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return fmt.Errorf("%w: the line does not begin one JSON object", ErrNoncanonicalTaskID)
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("%w: the line's members are not legible: %v", ErrNoncanonicalTaskID, err)
		}
		name, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return fmt.Errorf("%w: the line's members are not legible: %v", ErrNoncanonicalTaskID, err)
		}
		if strings.EqualFold(name, "task_id") && !lossless(v) {
			return fmt.Errorf("%w: the task identity %q is not written byte-faithfully (invalid UTF-8 or an "+
				"unpaired surrogate escape), so it would be read as an identity its bytes do not hold",
				ErrNoncanonicalTaskID, []byte(v))
		}
	}
	return nil
}

// lossless reports whether raw JSON bytes decode without replacement: valid
// UTF-8, and every \u escape of a surrogate one half of a well-formed pair.
func lossless(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	for i := 0; i < len(b); i++ {
		if b[i] != '\\' {
			continue
		}
		i++
		if i >= len(b) || b[i] != 'u' {
			continue
		}
		r, ok := hex4(b[i+1:])
		if !ok {
			return false
		}
		i += 4
		switch {
		case r >= 0xDC00 && r <= 0xDFFF:
			return false
		case r >= 0xD800 && r <= 0xDBFF:
			if i+2 >= len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
				return false
			}
			if lo, ok := hex4(b[i+3:]); !ok || lo < 0xDC00 || lo > 0xDFFF {
				return false
			}
			i += 6
		}
	}
	return true
}

// hex4 decodes the four hexadecimal digits a \u escape carries.
func hex4(b []byte) (rune, bool) {
	if len(b) < 4 {
		return 0, false
	}
	var r rune
	for _, c := range b[:4] {
		switch {
		case '0' <= c && c <= '9':
			r = r<<4 | rune(c-'0')
		case 'a' <= c && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		case 'A' <= c && c <= 'F':
			r = r<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return r, true
}

// The manifest's schema, version and the one status it may assert.
const (
	QuarantineSchema  = "sensei-code.session-quarantine"
	QuarantineVersion = 1
	// QuarantineAsserted is the status of a manifest: the record is excluded
	// from task reconstruction and its claims are reserved.
	QuarantineAsserted = "quarantined"
)

// QuarantineManifest is the durable, authenticated recovery assertion beside
// a torn session record. Every field is required; its bytes are its canonical
// encoding (encodeManifest) and nothing else.
type QuarantineManifest struct {
	Schema    string `json:"schema"`
	Version   int    `json:"version"`
	Status    string `json:"status"`
	SessionID string `json:"session_id"`
	// Record is the record's path relative to the repository, slash-separated.
	Record string `json:"record"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	// PrefixEnd is the complete-prefix boundary, and PrefixEvents the number
	// of events that prefix holds.
	PrefixEnd    int64 `json:"prefix_end"`
	PrefixEvents int   `json:"prefix_events"`
	// Claims is every task identity the record claims or may claim, sorted.
	Claims []string       `json:"claims"`
	Tail   QuarantineTail `json:"tail"`
	// AuthorizedBy is the recovery authorization identity, Provenance where
	// that authorization came from, and CreatedAt when the manifest was made
	// (RFC 3339, UTC).
	AuthorizedBy string `json:"authorized_by"`
	Provenance   string `json:"provenance"`
	CreatedAt    string `json:"created_at"`
}

// quarantinePath is the manifest beside the record at path.
func quarantinePath(path string) string {
	return filepath.Join(filepath.Dir(path), "quarantine.json")
}

// quarantineRecord is the record of sessionID relative to the repository, as
// a manifest names it.
func quarantineRecord(sessionID string) string {
	return ".sensei-code/sessions/" + sessionID + "/events.jsonl"
}

// encodeManifest is a manifest's one canonical encoding.
func encodeManifest(m QuarantineManifest) ([]byte, error) {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// decodeManifest decodes exactly one manifest with no unknown member, and
// refuses bytes that are not its canonical encoding.
func decodeManifest(raw []byte) (QuarantineManifest, error) {
	var m QuarantineManifest
	if err := DecodeExactlyOne(raw, &m); err != nil {
		return QuarantineManifest{}, fmt.Errorf("%w: it does not decode as one manifest: %v", ErrQuarantineInvalid, err)
	}
	canonical, err := encodeManifest(m)
	if err != nil {
		return QuarantineManifest{}, err
	}
	if !bytes.Equal(canonical, raw) {
		return QuarantineManifest{}, fmt.Errorf("%w: its bytes are not its canonical encoding", ErrQuarantineInvalid)
	}
	return m, nil
}

// validFor proves m is the recovery assertion of sessionID's record as sc
// classifies its current bytes: the schema, version, status and identity it
// must carry, an authorization and provenance, and the exact size, digest and
// classification of those bytes.
func (m QuarantineManifest) validFor(sessionID string, sc recordScan) error {
	created, terr := time.Parse(time.RFC3339Nano, m.CreatedAt)
	var fault string
	switch {
	case m.Schema != QuarantineSchema || m.Version != QuarantineVersion:
		fault = fmt.Sprintf("its schema is %q version %d, not %q version %d", m.Schema, m.Version, QuarantineSchema, QuarantineVersion)
	case m.Status != QuarantineAsserted:
		fault = fmt.Sprintf("its status is %q, not %q", m.Status, QuarantineAsserted)
	case m.SessionID != sessionID || m.Record != quarantineRecord(sessionID):
		fault = fmt.Sprintf("it names session %q at %q, not session %q at %q", m.SessionID, m.Record, sessionID, quarantineRecord(sessionID))
	case noncanonical(m.AuthorizedBy) != "":
		fault = "its recovery authorization identity " + noncanonical(m.AuthorizedBy)
	case noncanonical(m.Provenance) != "":
		fault = "its provenance " + noncanonical(m.Provenance)
	case terr != nil || created.UTC().Format(time.RFC3339Nano) != m.CreatedAt:
		fault = fmt.Sprintf("its creation time %q is not an RFC 3339 UTC instant", m.CreatedAt)
	case !sc.quarantinable():
		fault = fmt.Sprintf("the record beside it is %s, and %s", sc.class, sc.refusal)
	case m.Size != sc.size || m.SHA256 != sc.sha256:
		fault = fmt.Sprintf("it asserts %d bytes with SHA-256 %s, and the record is %d bytes with SHA-256 %s: it is stale",
			m.Size, m.SHA256, sc.size, sc.sha256)
	case m.PrefixEnd != sc.prefixEnd || m.PrefixEvents != len(sc.events):
		fault = fmt.Sprintf("it asserts a complete prefix of %d events ending at byte %d, and the record's is %d events ending at byte %d",
			m.PrefixEvents, m.PrefixEnd, len(sc.events), sc.prefixEnd)
	case m.Claims == nil || !slices.Equal(m.Claims, sc.claims):
		fault = fmt.Sprintf("it claims %q, and the record claims %q", m.Claims, sc.claims)
	case m.Tail != sc.tail:
		fault = fmt.Sprintf("it classifies the tail as %+v, and the record's is %+v", m.Tail, sc.tail)
	}
	if fault != "" {
		return fmt.Errorf("%w: the manifest of session %s: %s", ErrQuarantineInvalid, sessionID, fault)
	}
	return nil
}

// ledgerRecord is one session record as the task-claim inventory reads it:
// its events when it reads, its manifest when it is validly quarantined.
type ledgerRecord struct {
	sessionID  string
	path       string
	events     []event.Event
	quarantine *QuarantineManifest
	// claims is every task identity the record claims, exactly as admission
	// and discovery both compare it: a readable record's roots (rootClaims),
	// or a quarantined record's manifest claims.
	claims []string
}

// claimInventory is THE ONE INVENTORY of the task claims of every session
// record in the sessions directory: what admission (soleTaskLedger) and
// discovery (FindActive) both read, oldest first. A directory that holds no
// record holds nothing, unless a manifest is there (an orphan, which fails
// closed). Every record is read (readLedgerRecord), and one that cannot be
// read and is not validly quarantined is an error: never absence. held is the
// record whose lock the caller already holds, "" when none: it is read under
// that lock rather than locked again.
func claimInventory(ctx context.Context, sessions, held string) ([]ledgerRecord, error) {
	entries, err := os.ReadDir(sessions)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// No sessions directory is ABSENCE: this repository has begun no
			// task. An unreadable one is not.
			return nil, nil
		}
		return nil, fmt.Errorf("the session records in %s could not be listed, so what this repository holds is "+
			"unknown rather than absent: %w", sessions, err)
	}
	var out []ledgerRecord
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(sessions, entry.Name(), "events.jsonl")
		r, present, err := readLedgerRecord(ctx, path, held != "" && path == filepath.Clean(held))
		if err != nil {
			return nil, err
		}
		if present {
			out = append(out, r)
		}
	}
	return out, nil
}

// readLedgerRecord reads the record at path for the inventory. A record that
// reads is its events, and a manifest beside it is refused: it never
// overrides a readable record. A record that does not read on this unlocked
// read may be an append in flight, so it is RE-READ UNDER ITS OWN RECORD LOCK
// (bounded by recordLockWait, ended by ctx): readable then, it is used;
// still unreadable, it is excluded only by a manifest that is valid for the
// bytes read under that lock, and is otherwise refused (ErrRecordUnreadable).
// locked says the caller already holds the record's lock, so the first read
// is already the locked one and the lock is not taken again.
func readLedgerRecord(ctx context.Context, path string, locked bool) (ledgerRecord, bool, error) {
	r := ledgerRecord{sessionID: filepath.Base(filepath.Dir(path)), path: path}
	readable := func(sc recordScan) (ledgerRecord, bool, error) {
		if sc.claimErr != nil {
			return r, false, fmt.Errorf("session record %s: %w", path, sc.claimErr)
		}
		r.events, r.claims = sc.events, sc.claims
		return r, true, nil
	}
	sc, present, err := scanLedgerRecord(path)
	if err != nil || !present {
		return r, false, err
	}
	if sc.class == RecordReadable {
		return readable(sc)
	}
	if !locked {
		release, err := lockRecordFile(ctx, path, recordLockWait)
		if err != nil {
			return r, false, fmt.Errorf("%w: session record %s did not read (%s), and it could not be re-read under its lock, "+
				"so the tasks it holds are unknown rather than absent: %w", ErrRecordUnreadable, path, sc.refusal, err)
		}
		defer release()
		sc, present, err = scanLedgerRecord(path)
		if err != nil || !present {
			return r, false, err
		}
		if sc.class == RecordReadable {
			return readable(sc)
		}
	}
	raw, err := os.ReadFile(quarantinePath(path))
	if errors.Is(err, os.ErrNotExist) {
		return r, false, fmt.Errorf("%w: session record %s is %s -- %s -- so the tasks it holds are unknown rather "+
			"than absent", ErrRecordUnreadable, path, sc.class, sc.refusal)
	}
	if err != nil {
		return r, false, fmt.Errorf("%w: session record %s is %s, and its quarantine manifest could not be read: %w",
			ErrRecordUnreadable, path, sc.class, err)
	}
	m, err := decodeManifest(raw)
	if err == nil {
		err = m.validFor(r.sessionID, sc)
	}
	if err != nil {
		return r, false, fmt.Errorf("%w: session record %s is %s, and it is not quarantined: %w", ErrRecordUnreadable, path, sc.class, err)
	}
	r.quarantine, r.claims = &m, m.Claims
	return r, true, nil
}

// scanLedgerRecord reads and classifies the record at path. An absent record
// is absent only when no manifest stands beside it; a readable record with a
// manifest beside it is refused.
func scanLedgerRecord(path string) (recordScan, bool, error) {
	raw, err := os.ReadFile(path)
	manifest, merr := os.Lstat(quarantinePath(path))
	if merr != nil && !errors.Is(merr, os.ErrNotExist) {
		return recordScan{}, false, fmt.Errorf("whether session record %s is quarantined is unknown: %w", path, merr)
	}
	switch {
	case errors.Is(err, os.ErrNotExist):
		if manifest != nil {
			return recordScan{}, false, fmt.Errorf("%w: %s stands beside no session record: an orphan asserts nothing",
				ErrQuarantineInvalid, quarantinePath(path))
		}
		return recordScan{}, false, nil
	case err != nil:
		return recordScan{}, false, fmt.Errorf("%w: session record %s, so the tasks it holds are unknown rather than absent: %w",
			ErrRecordUnreadable, path, err)
	}
	sc := classifyRecord(raw)
	if sc.class == RecordReadable && manifest != nil {
		return recordScan{}, false, fmt.Errorf("%w: %s stands beside a session record that reads, and never overrides it",
			ErrQuarantineInvalid, quarantinePath(path))
	}
	return sc, true, nil
}

// QuarantineState is what a session record is with respect to quarantine.
type QuarantineState string

const (
	// QuarantineNone is a readable record with no manifest.
	QuarantineNone QuarantineState = "none"
	// QuarantineEligible is a torn record whose every claim is known and that
	// no manifest excludes yet: it still fails closed until activation.
	QuarantineEligible QuarantineState = "eligible"
	// QuarantineIneligible is an unreadable record that cannot be
	// quarantined: it fails closed.
	QuarantineIneligible QuarantineState = "ineligible"
	// QuarantineActive is a record a valid manifest excludes: its claims are
	// reserved and its tasks are visible but never resumable.
	QuarantineActive QuarantineState = "quarantined"
	// QuarantineInvalid is a manifest that is not a valid recovery assertion:
	// it excludes nothing, and the repository fails closed.
	QuarantineInvalid QuarantineState = "invalid"
)

// QuarantineInspection is one session record's quarantine condition, read
// without changing anything.
type QuarantineInspection struct {
	SessionID    string
	State        QuarantineState
	Class        RecordClass
	Size         int64
	SHA256       string
	PrefixEnd    int64
	PrefixEvents int
	Claims       []string
	Tail         QuarantineTail
	Manifest     *QuarantineManifest
	// Detail says why the record is not readable, cannot be quarantined, or
	// why its manifest is invalid.
	Detail string
}

// validSessionName refuses a session identity that is not one plain
// directory name.
func validSessionName(sessionID string) error {
	if reason := noncanonical(sessionID); reason != "" {
		return fmt.Errorf("session identity %q %s", sessionID, reason)
	}
	if sessionID == "." || sessionID == ".." || strings.ContainsAny(sessionID, `/\`) || filepath.Base(sessionID) != sessionID {
		return fmt.Errorf("session identity %q is not one session record's name", sessionID)
	}
	return nil
}

// InspectQuarantine is the read-only inspection of sessionID's record: its
// exact size, digest and classification, and its manifest's validity. It
// takes no lock and writes nothing.
func InspectQuarantine(repo, sessionID string) (QuarantineInspection, error) {
	if err := validSessionName(sessionID); err != nil {
		return QuarantineInspection{}, err
	}
	path := recordPath(repo, sessionID)
	in := QuarantineInspection{SessionID: sessionID}
	mraw, merr := os.ReadFile(quarantinePath(path))
	if merr != nil && !errors.Is(merr, os.ErrNotExist) {
		return in, merr
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && merr == nil {
		in.State, in.Detail = QuarantineInvalid, "the manifest stands beside no session record: an orphan asserts nothing"
		return in, nil
	}
	if err != nil {
		return in, err
	}
	sc := classifyRecord(raw)
	in.Class, in.Size, in.SHA256 = sc.class, sc.size, sc.sha256
	in.PrefixEnd, in.PrefixEvents, in.Claims, in.Tail = sc.prefixEnd, len(sc.events), sc.claims, sc.tail
	switch {
	case merr == nil:
		m, err := decodeManifest(mraw)
		if err == nil {
			in.Manifest = &m
			err = m.validFor(sessionID, sc)
		}
		if err != nil {
			in.State, in.Detail = QuarantineInvalid, err.Error()
		} else {
			in.State = QuarantineActive
		}
	case sc.class == RecordReadable:
		in.State = QuarantineNone
	case sc.quarantinable():
		in.State = QuarantineEligible
	default:
		in.State, in.Detail = QuarantineIneligible, sc.refusal
	}
	return in, nil
}

// QuarantineStatus is the inspection of every session record of the
// repository that is not simply readable, or has a manifest beside it: what
// is damaged, what is quarantined, and what fails closed. Read-only.
func QuarantineStatus(repo string) ([]QuarantineInspection, error) {
	sessions := filepath.Join(repo, ".sensei-code", "sessions")
	entries, err := os.ReadDir(sessions)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []QuarantineInspection
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := recordPath(repo, entry.Name())
		_, rerr := os.Lstat(path)
		_, merr := os.Lstat(quarantinePath(path))
		if errors.Is(rerr, os.ErrNotExist) && errors.Is(merr, os.ErrNotExist) {
			continue
		}
		if err := validSessionName(entry.Name()); err != nil {
			out = append(out, QuarantineInspection{SessionID: entry.Name(), State: QuarantineIneligible, Detail: err.Error()})
			continue
		}
		in, err := InspectQuarantine(repo, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("session record %s could not be inspected: %w", entry.Name(), err)
		}
		if in.State != QuarantineNone {
			out = append(out, in)
		}
	}
	return out, nil
}

// QuarantineAuthorization is what the human owner authorizes: the exact
// size and SHA-256 of the record they examined, who authorizes the recovery,
// and where that authorization came from.
type QuarantineAuthorization struct {
	ExpectedSize   int64
	ExpectedSHA256 string
	AuthorizedBy   string
	Provenance     string
}

// publishSeam is the test-observable seam of a manifest's publication
// (publishManifest). It is consulted at each step, BEFORE that step's real
// operation, which runs regardless: "write" once the manifest's bytes are
// written to the temporary file (f is that file); "sync" immediately before
// that file's real Sync (f is that file); "publish" before it is published
// (f is nil); "dirsync" immediately before the parent directory's real Sync
// (f is that directory); and "verify" before the published manifest is read
// back (f is nil). A non-nil error interrupts that step. The seam never
// stands in for a sync: it can only observe, or fail, the real one. It is a
// parameter, not shared state; production (ActivateQuarantine) passes none.
type publishSeam func(step string, f *os.File) error

// ActivateQuarantine is THE ONE WRITER of a quarantine manifest, at the
// explicit request of the human owner; nothing calls it on its own, and
// soleTaskLedger never does.
//
// The record is read once to learn its claims. Then, LEASE BEFORE RECORD
// LOCK -- the order every admission takes them in -- it takes the invocation
// lease of every claimed task, in sorted order, each bounded by
// taskLeaseWait, and then the record's own lock, bounded by recordLockWait;
// every wait ends with ctx. Under them it re-reads the record, and refuses
// unless it is torn with every claim known, its claims are exactly the ones
// leased, its size and SHA-256 are exactly the authorized ones, and no
// manifest exists. The manifest is published durably and exclusively
// (publishManifest) and read back and validated, all before the lock and
// leases are released. THE PUBLICATION IS THE LINEARIZATION POINT: before
// it, the record fails closed as it always did; after it, its claims are
// reserved and unrelated tasks proceed. No step releases a claim.
func ActivateQuarantine(ctx context.Context, repo, sessionID string, auth QuarantineAuthorization) (QuarantineManifest, error) {
	return activateQuarantine(ctx, repo, sessionID, auth, nil)
}

// activateQuarantine is ActivateQuarantine, its publication observed by seam
// when one is given (publishSeam); nil is production.
func activateQuarantine(ctx context.Context, repo, sessionID string, auth QuarantineAuthorization, seam publishSeam) (QuarantineManifest, error) {
	refuse := func(format string, args ...any) (QuarantineManifest, error) {
		return QuarantineManifest{}, fmt.Errorf("%w: session %s: %s", ErrQuarantineRefused, sessionID, fmt.Sprintf(format, args...))
	}
	if ctx == nil {
		return refuse("no caller context bounds the wait")
	}
	if err := validSessionName(sessionID); err != nil {
		return refuse("%v", err)
	}
	switch {
	case auth.ExpectedSize < 0:
		return refuse("no expected size is authorized")
	case len(auth.ExpectedSHA256) != sha256.Size*2 || strings.ToLower(auth.ExpectedSHA256) != auth.ExpectedSHA256:
		return refuse("the expected SHA-256 %q is not 64 lowercase hexadecimal digits", auth.ExpectedSHA256)
	case noncanonical(auth.AuthorizedBy) != "":
		return refuse("the recovery authorization identity %s", noncanonical(auth.AuthorizedBy))
	case noncanonical(auth.Provenance) != "":
		return refuse("the provenance %s", noncanonical(auth.Provenance))
	}
	if _, err := hex.DecodeString(auth.ExpectedSHA256); err != nil {
		return refuse("the expected SHA-256 %q is not hexadecimal", auth.ExpectedSHA256)
	}
	path := recordPath(repo, sessionID)
	s := &Store{path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		return refuse("the record could not be read: %v", err)
	}
	sc := classifyRecord(raw)
	if !sc.quarantinable() {
		return refuse("the record is %s: %s", sc.class, sc.refusal)
	}
	leased := sc.claims
	for _, taskID := range leased {
		lease := s.taskLeasePath(taskID)
		if err := os.MkdirAll(filepath.Dir(lease), 0o755); err != nil {
			return QuarantineManifest{}, err
		}
		release, err := lockFileWithin(ctx, lease, taskLeaseWait)
		if errors.Is(err, ErrRecordLockTimeout) {
			return QuarantineManifest{}, fmt.Errorf("%w: session %s: claimed task %s: %w: %w", ErrQuarantineRefused, sessionID,
				taskID, ErrTaskInvocationLeased, err)
		}
		if err != nil {
			return QuarantineManifest{}, fmt.Errorf("%w: session %s: claimed task %s: %w", ErrQuarantineRefused, sessionID, taskID, err)
		}
		defer release()
	}
	release, err := lockRecordFile(ctx, path, recordLockWait)
	if err != nil {
		return QuarantineManifest{}, fmt.Errorf("%w: session %s: %w", ErrQuarantineRefused, sessionID, err)
	}
	defer release()
	if raw, err = os.ReadFile(path); err != nil {
		return refuse("the record could not be re-read under its lock: %v", err)
	}
	sc = classifyRecord(raw)
	switch {
	case !sc.quarantinable():
		return refuse("the record, re-read under its lock, is %s: %s", sc.class, sc.refusal)
	case !slices.Equal(sc.claims, leased):
		return refuse("the record, re-read under its lock, claims %q, not the %q whose leases are held", sc.claims, leased)
	case sc.size != auth.ExpectedSize || sc.sha256 != auth.ExpectedSHA256:
		return QuarantineManifest{}, fmt.Errorf("%w: session %s is %d bytes with SHA-256 %s, and %d bytes with SHA-256 %s "+
			"were authorized; nothing was published", ErrQuarantineMismatch, sessionID, sc.size, sc.sha256,
			auth.ExpectedSize, auth.ExpectedSHA256)
	}
	final := quarantinePath(path)
	if _, err := os.Lstat(final); err == nil {
		return refuse("a manifest already stands beside the record, and a manifest is never overwritten")
	} else if !errors.Is(err, os.ErrNotExist) {
		return refuse("whether a manifest already stands beside the record is unknown: %v", err)
	}
	m := QuarantineManifest{
		Schema: QuarantineSchema, Version: QuarantineVersion, Status: QuarantineAsserted,
		SessionID: sessionID, Record: quarantineRecord(sessionID), Size: sc.size, SHA256: sc.sha256,
		PrefixEnd: sc.prefixEnd, PrefixEvents: len(sc.events), Claims: sc.claims, Tail: sc.tail,
		AuthorizedBy: auth.AuthorizedBy, Provenance: auth.Provenance,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	encoded, err := encodeManifest(m)
	if err != nil {
		return QuarantineManifest{}, err
	}
	published, err := publishManifest(final, encoded, seam)
	if err != nil && !published {
		return QuarantineManifest{}, fmt.Errorf("%w: session %s: the manifest was not published: %w", ErrQuarantineRefused, sessionID, err)
	}
	if err == nil {
		err = verifyManifest(final, encoded, sessionID, sc, seam)
	}
	if err != nil {
		return QuarantineManifest{}, fmt.Errorf("%w: session %s: %w", ErrQuarantineIndeterminate, sessionID, err)
	}
	return m, nil
}

// publishManifest publishes data at final DURABLY AND EXCLUSIVELY: written
// to a temporary file created exclusively beside it, synced and closed, then
// hard-linked to final -- which fails, in any process, when final already
// exists, so no manifest is ever replaced -- and the directory synced. The
// file's Sync precedes the publication, and the directory's Sync precedes
// success; a failure of either is ErrQuarantineSync. published reports that
// final was created; an error after it leaves a manifest whose durability is
// unconfirmed. seam, when given, observes each step (publishSeam).
func publishManifest(final string, data []byte, seam publishSeam) (published bool, err error) {
	if seam == nil {
		seam = func(string, *os.File) error { return nil }
	}
	dir := filepath.Dir(final)
	tmp, err := os.CreateTemp(dir, "quarantine.*.tmp")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	n, err := tmp.Write(data)
	if err == nil && n != len(data) {
		err = fmt.Errorf("short write: %d of %d bytes", n, len(data))
	}
	if err == nil {
		err = seam("write", tmp)
	}
	if err == nil {
		if err = seam("sync", tmp); err == nil {
			if err = tmp.Sync(); err != nil {
				err = fmt.Errorf("%w: the manifest file: %w", ErrQuarantineSync, err)
			}
		}
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = seam("publish", nil)
	}
	if err != nil {
		return false, err
	}
	if err := os.Link(tmp.Name(), final); err != nil {
		return false, err
	}
	if err := os.Remove(tmp.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return true, err
	}
	d, err := os.Open(dir)
	if err != nil {
		return true, fmt.Errorf("%w: the manifest's directory: %w", ErrQuarantineSync, err)
	}
	defer d.Close()
	if err := seam("dirsync", d); err != nil {
		return true, err
	}
	if err := d.Sync(); err != nil {
		return true, fmt.Errorf("%w: the manifest's directory: %w", ErrQuarantineSync, err)
	}
	return true, nil
}

// verifyManifest reads the published manifest back and requires it to be
// exactly data, and valid for the record as sc classifies it.
func verifyManifest(final string, data []byte, sessionID string, sc recordScan, seam publishSeam) error {
	if seam != nil {
		if err := seam("verify", nil); err != nil {
			return err
		}
	}
	raw, err := os.ReadFile(final)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, data) {
		return fmt.Errorf("%w: the published manifest is not the bytes that were written", ErrQuarantineInvalid)
	}
	m, err := decodeManifest(raw)
	if err != nil {
		return err
	}
	return m.validFor(sessionID, sc)
}

// QuarantineCommand is the operator's `sensei-code quarantine` command:
// inspect one record, activate its quarantine with the owner's exact
// authorization, or report the status of every damaged or quarantined
// record. It parses and renders; every decision is the Store's.
func QuarantineCommand(ctx context.Context, repo string, args []string, out io.Writer) error {
	const usage = "usage: sensei-code quarantine status | inspect --session <id> | activate --session <id> " +
		"--expected-size <bytes> --expected-sha256 <hex> --authorized-by <identity> --provenance <text>"
	if len(args) == 0 {
		return errors.New(usage)
	}
	fs := flag.NewFlagSet("quarantine "+args[0], flag.ContinueOnError)
	fs.SetOutput(out)
	sessionID := fs.String("session", "", "the session record")
	size := fs.Int64("expected-size", -1, "the record's exact size the owner authorized, in bytes")
	digest := fs.String("expected-sha256", "", "the record's exact SHA-256 the owner authorized")
	by := fs.String("authorized-by", "", "the recovery authorization identity")
	provenance := fs.String("provenance", "", "where the authorization came from")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments %q; %s", fs.Args(), usage)
	}
	switch args[0] {
	case "status":
		list, err := QuarantineStatus(repo)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Fprintln(out, "No session record of this repository is damaged or quarantined.")
		}
		for _, in := range list {
			renderInspection(out, in)
		}
		return nil
	case "inspect":
		if *sessionID == "" {
			return errors.New(usage)
		}
		in, err := InspectQuarantine(repo, *sessionID)
		if err != nil {
			return err
		}
		renderInspection(out, in)
		return nil
	case "activate":
		if *sessionID == "" || *size < 0 || *digest == "" || *by == "" || *provenance == "" {
			return errors.New(usage)
		}
		m, err := ActivateQuarantine(ctx, repo, *sessionID, QuarantineAuthorization{
			ExpectedSize: *size, ExpectedSHA256: *digest, AuthorizedBy: *by, Provenance: *provenance})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Session %s is quarantined: %d bytes, SHA-256 %s, preserved in place.\n", m.SessionID, m.Size, m.SHA256)
		fmt.Fprintf(out, "Reserved task claims: %s. They are never minted or resumed again.\n", strings.Join(m.Claims, ", "))
		return nil
	}
	return errors.New(usage)
}

// renderInspection writes one inspection for the operator.
func renderInspection(out io.Writer, in QuarantineInspection) {
	fmt.Fprintf(out, "session %s: %s\n", in.SessionID, in.State)
	if in.Class != "" {
		fmt.Fprintf(out, "  record: %s, %d bytes, SHA-256 %s\n", in.Class, in.Size, in.SHA256)
		fmt.Fprintf(out, "  complete prefix: %d events, %d bytes\n", in.PrefixEvents, in.PrefixEnd)
		fmt.Fprintf(out, "  task claims: %s\n", strings.Join(in.Claims, ", "))
	}
	if in.Tail.Size > 0 {
		fmt.Fprintf(out, "  torn tail: %d bytes, kind %q, task %q\n", in.Tail.Size, in.Tail.Kind, in.Tail.TaskID)
	}
	if m := in.Manifest; m != nil {
		fmt.Fprintf(out, "  manifest: authorized by %s at %s (%s)\n", m.AuthorizedBy, m.CreatedAt, m.Provenance)
	}
	if in.State == QuarantineActive {
		fmt.Fprintln(out, "  its tasks are reserved and are not resumable")
	}
	if in.Detail != "" {
		fmt.Fprintf(out, "  %s\n", in.Detail)
	}
}

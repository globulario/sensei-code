package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/globulario/sensei-code/internal/event"
)

type Store struct {
	mu   sync.Mutex
	path string
}

func New(repo, sessionID string) (*Store, error) {
	p := filepath.Join(repo, ".sensei-code", "sessions", sessionID, "events.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	return &Store{path: p}, nil
}

func (s *Store) Append(e event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(e)
}

func (s *Store) Load() ([]event.Event, error) {
	f, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []event.Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e event.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// ID mints a session identifier that sorts chronologically as a string, so the
// most recent session can be found without reading any file.
func ID(t time.Time) string {
	return "session-" + t.UTC().Format("20060102T150405.000000000Z")
}

// Latest returns the most recent recorded session for this repository, so a
// relaunch can continue the conversation instead of starting blank.
func Latest(repo string) (string, bool) {
	dir := filepath.Join(repo, ".sensei-code", "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var ids []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, entry.Name(), "events.jsonl")); err != nil {
			continue
		}
		ids = append(ids, entry.Name())
	}
	if len(ids) == 0 {
		return "", false
	}
	sort.Strings(ids)
	return ids[len(ids)-1], true
}

// Interrupted is a task that was approved and started but never reached a
// terminal event, because the process died or was killed while a worker was
// running.
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
}

// FindInterrupted recovers tasks that were left mid-flight, from the session
// record rather than from a second bookkeeping file. The log is already the
// account of what happened; a parallel state file could disagree with it, and
// then neither could be trusted.
func FindInterrupted(events []event.Event) []Interrupted {
	type partial struct {
		Interrupted
		planned  bool
		deferred bool
		done     bool
	}
	order := []string{}
	byTask := map[string]*partial{}
	get := func(id string) *partial {
		if id == "" {
			return nil
		}
		if _, ok := byTask[id]; !ok {
			byTask[id] = &partial{Interrupted: Interrupted{TaskID: id}}
			order = append(order, id)
		}
		return byTask[id]
	}
	for _, e := range events {
		p := get(e.TaskID)
		if p == nil {
			continue
		}
		switch e.Kind {
		case event.TaskCreated:
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
			p.PlanEventSource = e.Source
			var src struct {
				Source string `json:"plan_source"`
				Digest string `json:"plan_digest"`
			}
			if len(e.Payload) != 0 && json.Unmarshal(e.Payload, &src) == nil {
				p.PlanSource, p.PlanDigest = src.Source, src.Digest
			}
		case event.ProspectiveGranted:
			p.ProspectiveRecord = e.Payload
		case event.WorkflowCompleted, event.WorkflowFailed:
			p.done = true
		case event.WorkflowStopped:
			// Deliberately not terminal. A stop is the human withdrawing
			// attention, and the whole point of leaving the candidate as it
			// stands is that it can be picked back up.
		case event.WorkflowAwaitingAuthority:
			// Also not terminal, and resumable even with no plan: a question
			// deferred during architecture is the ordinary case, and it is
			// reached before any plan exists.
			p.deferred = true
			p.AwaitingAuthority = e.Payload
		case event.AuthorityResolved:
			// The standing question was answered, so there is nothing left to
			// restore. Clearing it matters: a task deferred, resumed, answered
			// and interrupted again must resume as work, not as the question
			// it already settled.
			p.deferred = false
			p.AwaitingAuthority = nil

		}
		if e.Source == event.SourceReviewer && e.Kind == event.Status && strings.TrimSpace(e.Summary) != "" {
			p.Review = e.Summary
		}
	}
	var out []Interrupted
	for _, id := range order {
		p := byTask[id]
		if (p.planned || p.deferred) && !p.done && strings.TrimSpace(p.Task) != "" {
			out = append(out, p.Interrupted)
		}
	}
	return out
}

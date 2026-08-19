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
	// Review is the last thing the reviewer said, which is the most useful
	// thing to hand whoever picks the work up.
	Review string
}

// FindInterrupted recovers tasks that were left mid-flight, from the session
// record rather than from a second bookkeeping file. The log is already the
// account of what happened; a parallel state file could disagree with it, and
// then neither could be trusted.
func FindInterrupted(events []event.Event) []Interrupted {
	type partial struct {
		Interrupted
		planned bool
		done    bool
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
		case event.WorkflowCompleted, event.WorkflowFailed:
			p.done = true
		case event.WorkflowStopped:
			// Deliberately not terminal. A stop is the human withdrawing
			// attention, and the whole point of leaving the candidate as it
			// stands is that it can be picked back up.

		}
		if e.Source == event.SourceReviewer && e.Kind == event.Status && strings.TrimSpace(e.Summary) != "" {
			p.Review = e.Summary
		}
	}
	var out []Interrupted
	for _, id := range order {
		p := byTask[id]
		if p.planned && !p.done && strings.TrimSpace(p.Task) != "" {
			out = append(out, p.Interrupted)
		}
	}
	return out
}

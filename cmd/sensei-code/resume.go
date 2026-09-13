package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/globulario/sensei-code/internal/authority"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/workflow"
)

// Answering a preserved human-owned question without a TUI.
//
// `ea89e31` made an unattended run DEFER a Level-3 question instead of parking
// on it forever, and sensei#353 proved that half works on a live case: the
// architect escalated, the run deferred, and the question was persisted whole.
// Answering it then did nothing, because `Engine.ResolveHuman` was reachable
// only from `internal/tui/model.go`. The adopted decision had to be supplied as
// INPUT to a fresh governed task — a commissioning workaround that discards the
// task, the plan, the candidate and the question's own identity.
//
// This is the other half. It resumes THE SAME task through the same owner:
//
//	                Engine.awaitChoice  <-- the one rendezvous
//	                        ▲
//	          ┌─────────────┴─────────────┐
//	   ResolveHuman                 DeferAuthority
//	    ▲          ▲                  ▲        ▲
//	   TUI      resume              run    control
//
// It adds no event kind, no artifact and no waiter. The question was already
// durable; what was missing was a surface that could find it and carry a
// person's answer to it.

// Refusals. Each names the party that is actually wrong, because "resume
// failed" sends a person to read code when the real fact is usually that they
// named a task that is not asking anything.
var (
	errNoSession        = errors.New("this repository has no session, so no question can be standing in one")
	errTaskUnknown      = errors.New("no interrupted task with that id is recorded in the latest session")
	errNoQuestion       = errors.New("that task is interrupted but is not waiting on a human-owned decision, so there is nothing to answer")
	errQuestionUnusable = errors.New("that task's preserved question could not be read back, so it cannot be answered")
	errNoOptions        = errors.New("that task's preserved question carried no options, so no answer to it exists")
	errNotAnOption      = errors.New("that is not one of the options the preserved question offered")
)

// authorityResume is the exact delivery one invocation is entitled to make: one
// task, the question as it was recorded, and one option that question offered.
type authorityResume struct {
	Task     session.Interrupted
	Question workflow.DeferredAuthority
	Option   authority.Option
}

// selectAuthorityResume decides whether a supplied answer may be delivered at
// all, from the durable record alone.
//
// Pure, and separated from every side effect, because the refusals are the
// valuable part: an unbound, stale, or mismatched answer must be refused before
// a process, a graph or a provider is involved. It never falls back, never picks
// a task when the id does not match one, and never picks an option when the
// answer does not name one.
func selectAuthorityResume(tasks []session.Interrupted, taskID, answer string) (authorityResume, error) {
	taskID = strings.TrimSpace(taskID)
	answer = strings.TrimSpace(answer)
	var found *session.Interrupted
	for i := range tasks {
		if tasks[i].TaskID == taskID {
			found = &tasks[i]
			break
		}
	}
	if found == nil {
		return authorityResume{}, fmt.Errorf("%w: %s", errTaskUnknown, taskID)
	}
	if len(found.AwaitingAuthority) == 0 {
		return authorityResume{}, fmt.Errorf("%w: %s", errNoQuestion, taskID)
	}
	var q workflow.DeferredAuthority
	if err := json.Unmarshal(found.AwaitingAuthority, &q); err != nil {
		// Deliberately not "assume it is answerable". A question that cannot be
		// read back is not a question with unknown options; it is a record this
		// invocation must not act on.
		return authorityResume{}, fmt.Errorf("%w: %v", errQuestionUnusable, err)
	}
	if len(q.Decision.Options) == 0 {
		return authorityResume{}, fmt.Errorf("%w: %s", errNoOptions, taskID)
	}
	for _, option := range q.Decision.Options {
		if option.ID == answer {
			return authorityResume{Task: *found, Question: q, Option: option}, nil
		}
	}
	return authorityResume{}, fmt.Errorf("%w: %q; the question offered %s",
		errNotAnOption, answer, optionIDs(q.Decision.Options))
}

func optionIDs(options []authority.Option) string {
	ids := make([]string, 0, len(options))
	for _, option := range options {
		ids = append(ids, strconv.Quote(option.ID))
	}
	return strings.Join(ids, ", ")
}

// humanAnswer carries one person's answer to the settle loop and delivers it
// through the engine's own door.
//
// It embeds runControl rather than reimplementing it: withdrawing attention,
// stopping and timing out are unchanged by the fact that this invocation can
// also answer. Only AnswerAuthority is new, and only this type has it.
type humanAnswer struct {
	runControl
	resolve func(taskID, optionID string) bool
	option  authority.Option
	warn    io.Writer

	mu    sync.Mutex
	spent bool
}

// AnswerAuthority delivers the answer to the question it was validated against,
// exactly once.
//
// Two guards, and neither is decoration:
//
// The LIVE payload is checked, not only the durable record. `resumeAuthority`
// re-asks the recorded question byte for byte, so today the two always agree —
// which is exactly why the check is cheap to keep and would be expensive to
// discover missing. If any future change re-derives the question, an answer to
// the old one stops being deliverable instead of silently satisfying a boundary
// nobody was asked about.
//
// SPENT-ONCE is identity, not rate limiting. The answer belongs to one question.
// A second AuthorityRequired in the same run is a second human-owned boundary,
// and this invocation has no answer for it: it reports false, the settle loop
// defers, and the new question is preserved exactly as `ea89e31` established.
func (h *humanAnswer) AnswerAuthority(taskID string, ev event.Event) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.spent {
		fmt.Fprintln(h.warn, "sensei-code resume: a second human-owned decision was reached; "+
			"this answer belongs to the first question, so the new one is preserved, not answered")
		return false
	}
	if !liveQuestionOffers(ev, h.option.ID) {
		fmt.Fprintln(h.warn, "sensei-code resume: the question this run asked does not offer the answered option; "+
			"the question is preserved, not answered")
		return false
	}
	if !h.resolve(taskID, h.option.ID) {
		// The engine reports that this task is not waiting on a decision. Say
		// so instead of retrying: an answer that cannot be delivered leaves the
		// question standing, which is the safe direction.
		fmt.Fprintln(h.warn, "sensei-code resume: the engine is not waiting on a decision for this task; "+
			"the answer was not delivered")
		return false
	}
	h.spent = true
	fmt.Fprintln(h.warn, "sensei-code resume: the preserved question was answered by the person who ran this command: "+
		h.option.ID+": "+h.option.Label)
	return true
}

// liveQuestionOffers reports whether the question the run just asked actually
// offers this option. An unreadable or optionless payload is not an agreement.
func liveQuestionOffers(ev event.Event, optionID string) bool {
	raw, err := json.Marshal(ev.Payload)
	if err != nil {
		return false
	}
	var decision authority.Decision
	if err := json.Unmarshal(raw, &decision); err != nil {
		return false
	}
	for _, option := range decision.Options {
		if option.ID == optionID {
			return true
		}
	}
	return false
}

// resumeAuthorityAnswered is `sensei-code resume`.
func resumeAuthorityAnswered(ctx context.Context, repo gitx.Repo, cfg config.Config, args []string) int {
	fs := flag.NewFlagSet("resume", flag.ExitOnError)
	taskID := fs.String("task", "", "the exact task whose preserved question is being answered")
	answer := fs.String("answer", "", "the option id, taken from that question's own options")
	list := fs.Bool("list", false, "print the human-owned questions standing in this repository and exit")
	// A deferred question outlives the session it was asked in, and sessions
	// keep being created. Defaulting to the latest and offering no way to name
	// another would mean a question became unanswerable because something
	// unrelated ran afterwards -- which is the parking failure this command
	// exists to end, moved one layer out.
	sessionName := fs.String("session", "", "the session holding the question; defaults to the most recent one with a record")
	timeout := fs.Duration("timeout", 0, "give up after this long; 0 waits indefinitely")
	asJSON := fs.Bool("json", false, "emit the event stream as JSONL instead of prose")
	quiet := fs.Bool("quiet", false, "print only terminal outcomes")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	sessionID := strings.TrimSpace(*sessionName)
	if sessionID == "" {
		latest, ok := session.Latest(repo.Root)
		if !ok {
			fmt.Fprintln(os.Stderr, "sensei-code resume:", errNoSession)
			return exitUsage
		}
		sessionID = latest
	}
	// The SAME session, opened for append. A resumed task continues one account
	// of what happened; a new session log would leave the question in one file
	// and its answer in another.
	store, err := session.New(repo.Root, sessionID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code resume:", err)
		return exitFailed
	}
	history, err := store.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code resume: could not read the session record:", err)
		return exitFailed
	}
	interrupted := session.FindInterrupted(history)

	if *list {
		printStandingQuestions(os.Stdout, sessionID, interrupted)
		if *sessionName == "" && countStanding(interrupted) == 0 {
			// The default session is the newest, which is rarely the one that
			// deferred. Say where else to look rather than letting "none" read
			// as "there are none".
			reportOtherSessionsWithQuestions(os.Stdout, repo.Root, sessionID)
		}
		return exitCompleted
	}
	if strings.TrimSpace(*taskID) == "" || strings.TrimSpace(*answer) == "" {
		fmt.Fprintln(os.Stderr, "sensei-code resume: --task and --answer are both required; "+
			"run `sensei-code resume --list` to see the questions standing and the options each offers")
		return exitUsage
	}

	// Decided from the durable record before anything is started. A refusal here
	// costs nothing and starts nothing.
	target, err := selectAuthorityResume(interrupted, *taskID, *answer)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code resume:", err)
		return exitUsage
	}

	// The same readiness gate run applies, for the same reason.
	if report := inspectQuick(ctx, repo, cfg); !report.Ready() {
		fmt.Fprintln(os.Stderr, report.Render())
		fmt.Fprint(os.Stderr, "\nThis repository is not ready. Run:\n\n    sensei-code setup --apply\n\n")
		return exitFailed
	}

	bus := event.NewBus()
	events, unsubscribe := bus.Subscribe(512)
	defer unsubscribe()
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}
	engine := workflow.New(repo, cfg, bus, store, sessionID)

	// The bridge, for the reason run installs it: what happens AFTER the answer
	// is ordinary governed work, and it needs the architect and reviewer turns
	// to travel the configured transport rather than the local provider.
	if banner, err := installGitHubBridge(engine, repo.Root, cfg.GitHubBridge); err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code resume:", err)
		return exitFailed
	} else if banner != "" {
		fmt.Fprintln(os.Stderr, banner)
	}

	if !*quiet {
		fmt.Printf("resuming task %s  session %s\n", target.Task.TaskID, sessionID)
		fmt.Printf("question  %s\n", strings.TrimSpace(target.Question.Decision.Subject))
		fmt.Printf("answer    %s: %s\n", target.Option.ID, target.Option.Label)
	}

	answered := &humanAnswer{
		runControl: engine,
		resolve:    engine.ResolveHuman,
		option:     target.Option,
		warn:       os.Stderr,
	}
	// Resume re-asks the recorded question and, once answered, continues THIS
	// task. It is not a fresh submission: no preflight, no start gate, no
	// router, and the task, plan and candidate are the ones already bound.
	resumedID := engine.Resume(ctx, target.Task)
	return streamUntilSettled(ctx, answered, events, resumedID, *asJSON, *quiet, *timeout)
}

// printStandingQuestions shows what may be answered, and the option ids to
// answer it with. Without this a person has to read the event log to find out
// what this command will accept.
func printStandingQuestions(out io.Writer, sessionID string, tasks []session.Interrupted) {
	standing := 0
	for _, task := range tasks {
		if len(task.AwaitingAuthority) == 0 {
			continue
		}
		var q workflow.DeferredAuthority
		if err := json.Unmarshal(task.AwaitingAuthority, &q); err != nil {
			standing++
			fmt.Fprintf(out, "task %s\n  the preserved question could not be read back: %v\n\n", task.TaskID, err)
			continue
		}
		standing++
		fmt.Fprintf(out, "task %s\n", task.TaskID)
		if s := strings.TrimSpace(task.Task); s != "" {
			fmt.Fprintf(out, "  objective  %s\n", s)
		}
		fmt.Fprintf(out, "  question   %s\n", strings.TrimSpace(q.Decision.Subject))
		if s := strings.TrimSpace(q.Condition); s != "" {
			fmt.Fprintf(out, "  condition  %s\n", s)
		}
		if s := strings.TrimSpace(q.Decision.Reason); s != "" {
			fmt.Fprintf(out, "  reason     %s\n", s)
		}
		for _, option := range q.Decision.Options {
			fmt.Fprintf(out, "  --answer %-4s %s\n", option.ID, option.Label)
		}
		fmt.Fprintln(out)
	}
	if standing == 0 {
		fmt.Fprintf(out, "session %s: no human-owned question is standing\n", sessionID)
		return
	}
	fmt.Fprintf(out, "session %s: %d standing\n", sessionID, standing)
}

// countStanding is how many of these tasks are actually asking something.
func countStanding(tasks []session.Interrupted) int {
	n := 0
	for _, task := range tasks {
		if len(task.AwaitingAuthority) != 0 {
			n++
		}
	}
	return n
}

// reportOtherSessionsWithQuestions points at sessions that DO hold a standing
// question, so an empty default listing is not read as "there are none".
//
// It reads the recorded event kinds only — it does not decide anything and does
// not resume anything. Naming a session is the person's act, which is why
// --session exists rather than this picking one.
func reportOtherSessionsWithQuestions(out io.Writer, root, skip string) {
	dir := filepath.Join(root, ".sensei-code", "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var others []string
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == skip {
			continue
		}
		store, err := session.New(root, entry.Name())
		if err != nil {
			continue
		}
		history, err := store.Load()
		if err != nil {
			continue
		}
		if n := countStanding(session.FindInterrupted(history)); n > 0 {
			others = append(others, fmt.Sprintf("  --session %s   (%d standing)", entry.Name(), n))
		}
	}
	if len(others) == 0 {
		return
	}
	sort.Strings(others)
	fmt.Fprintf(out, "\nOther sessions hold standing questions:\n%s\n", strings.Join(others, "\n"))
}

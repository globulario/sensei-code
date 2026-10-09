package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/globulario/sensei-code/internal/authority"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/ghbridge"
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
	errTaskUnknown      = errors.New("no active task with that id is recorded in any session of this repository")
	errTaskNotInSession = errors.New("that task is active, but not in the session named")
	errNoQuestion       = errors.New("that task is interrupted but is not waiting on a human-owned decision, so there is nothing to answer")
	errQuestionUnusable = errors.New("that task's preserved question could not be read back, so it cannot be answered")
	errNoOptions        = errors.New("that task's preserved question carried no options, so no answer to it exists")
	errNotAnOption      = errors.New("that is not one of the options the preserved question offered")
	errBoundElsewhere   = errors.New("the preserved question is bound to a different task than the one named")
	errUnprovableScope  = errors.New("that question cannot prove which files it was asked about")
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
	// A task whose history cannot be read as its own lineage asks nothing
	// anyone can answer: its question, if it has one, is unknown.
	if found.Unavailable != nil {
		return authorityResume{}, fmt.Errorf("%w: %v", errTaskHistoryUnavailable, found.Unavailable)
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
	// A record that NAMES a task must name this one. A record that names none
	// predates the field: absence is not disagreement, and reading it as one
	// would make every question deferred before that field unanswerable.
	if q.TaskID != "" && q.TaskID != taskID {
		return authorityResume{}, fmt.Errorf("%w: record says %s, you named %s", errBoundElsewhere, q.TaskID, taskID)
	}
	for _, option := range q.Decision.Options {
		if option.ID == answer {
			return authorityResume{Task: *found, Question: q, Option: option}, nil
		}
	}
	return authorityResume{}, fmt.Errorf("%w: %q; the question offered %s",
		errNotAnOption, answer, optionIDs(q.Decision.Options))
}

// resumingSession is the fresh SessionID a fresh process continuing an
// existing session record acts under, whichever entrypoint started it -- the
// resume command and the interactive startup alike. It is minted by the one
// production identity generator (session.FreshID), never the clock alone, so
// two processes resuming in the same instant cannot share an identity.
func resumingSession(now time.Time) (string, error) {
	id, err := session.FreshID(now)
	if err != nil {
		return "", fmt.Errorf("no fresh session identity can be minted for the resumed process: %w", err)
	}
	return id, nil
}

// openResumeIdentity is the identity ONE resume invocation acts under,
// whichever lane it takes: the holder's existing record -- one physical
// ledger, opened under the session that holds it -- and a fresh SessionID
// minted for this process (resumingSession), never the holder's. It is called
// once, before the lane is chosen, so every lane -- interrupted work, a
// blocked turn, an owed review, an answered question -- is handed the same
// fresh session, which Engine.ResumeTask then binds through the task's
// lineage before the lane's first ordinary event.
func openResumeIdentity(root, holderSessionID string, now time.Time) (*session.Store, string, error) {
	store, err := session.New(root, holderSessionID)
	if err != nil {
		return nil, "", err
	}
	fresh, err := resumingSession(now)
	if err != nil {
		return nil, "", err
	}
	if fresh == holderSessionID {
		return nil, "", fmt.Errorf("the session minted for this resume is the holder's own (%s); a resumed process acts "+
			"under a fresh session", holderSessionID)
	}
	return store, fresh, nil
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

	// EVERY session record. ALWAYS. `--session` narrows the ANSWER, afterwards.
	//
	// A task outlives the process that began it, so the session that holds it is
	// rarely the newest. Reading only the latest record is what made an active
	// task answer "no interrupted task with that id" the moment anything else
	// ran. Discovery fails closed: an unreadable history or two records claiming
	// one task id end this command rather than shrinking the world it searched.
	//
	// `--session` USED TO BRANCH AWAY FROM THAT, into a reader that opened the
	// named record alone. Both of discovery's refusals were then unreachable for
	// the path a person takes when they know which session they mean: another
	// record claiming the same task id was invisible, and a history elsewhere
	// that nobody could open was never met. Naming one side of a split identity
	// selected it. A name is not a scope -- it is a filter over a world that has
	// already been validated whole, which is why the narrowing happens below and
	// through Discovery itself.
	//
	// This is also the one read that says whether this repository has any
	// history at all. There used to be a session.Latest precheck in front of it
	// answering a boolean: a sessions directory that could not be listed, or a
	// record that could not be stat'd, came back false and this command printed
	// "no session in this repository" -- absence, stated from a history nobody
	// had managed to open, about storage that might hold the very task being
	// looked for.
	scoped := strings.TrimSpace(*sessionName)
	inventory, err := session.FindActive(repo.Root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code resume:", err)
		return exitFailed
	}
	if len(inventory.Records) == 0 {
		fmt.Fprintln(os.Stderr, "sensei-code resume:", errNoSession)
		return exitUsage
	}
	active := inventory.Active
	if scoped != "" {
		narrowed, err := inventory.ResumableIn(scoped)
		if err != nil {
			// A session nobody recorded is the person's mistake, not a storage
			// failure: discovery read every record this repository holds and
			// none of them is the one named.
			fmt.Fprintln(os.Stderr, "sensei-code resume:", err)
			return exitUsage
		}
		active = narrowed
	}

	if *list {
		// The durable review obligations are established BEFORE anything is
		// printed, and a store failure ends the command. A listing that could
		// not read what a task owes must not render a guess about it.
		owed, err := loadReviewStates(repo.Root, active)
		if err != nil {
			fmt.Fprintln(os.Stderr, "sensei-code resume:", err)
			return exitFailed
		}
		printActiveTasks(os.Stdout, scoped, active, owed)
		if scoped == "" {
			// A quarantined record's tasks are reserved, visible and never
			// resumable: listed as such, never as absent.
			printQuarantinedTasks(os.Stdout, inventory.Quarantined)
		}
		if scoped != "" && countStanding(tasksOf(active)) == 0 {
			// A named session is a window onto one account. Say where else to
			// look rather than letting "none here" read as "there are none".
			// The hint is drawn from the inventory the listing itself came
			// from, so it speaks about the same world.
			reportOtherSessionsWithQuestions(os.Stdout, inventory.Active, scoped)
		}
		return exitCompleted
	}
	if strings.TrimSpace(*taskID) == "" {
		fmt.Fprintln(os.Stderr, "sensei-code resume: --task is required; "+
			"run `sensei-code resume --list` to see the tasks still active and what each one owes")
		return exitUsage
	}
	// The record that HOLDS the task is the one this continuation appends to. A
	// resumed task continues one account of what happened; writing into the
	// newest session instead would leave the question in one file and its answer
	// in another.
	// REFUSAL PRECEDES SELECTION. A task some record holds with a history
	// that cannot be read as its own session lineage, and a task a
	// quarantined record claims, are refused here, typed, before any lane or
	// answer is considered -- and before the ordinary miss below could call
	// either of them absent. Read across the whole inventory, not the scoped
	// view: a session filter does not waive what the repository holds. The
	// TUI's /resume reads the same refusal (Discovery.ResumableIn).
	if refusing, refusal := inventory.TaskRefusal(*taskID); refusal != nil {
		fmt.Fprintln(os.Stderr, "sensei-code resume:", taskRefusalError(refusing, refusal))
		return exitFailed
	}
	holder, held := activeTask(active, *taskID)
	if !held {
		// A SCOPED MISS IS NOT A REPOSITORY-WIDE ABSENCE. errTaskUnknown speaks
		// about every session there is, and after `--session` narrowed the
		// lookup that sentence would be stated about records this lookup never
		// consulted -- while the inventory in hand can name the one that holds
		// the task. Saying the wider thing is the same confident disappearance
		// this command exists to end, one layer in.
		if elsewhere, anywhere := activeTask(inventory.Active, *taskID); anywhere {
			fmt.Fprintf(os.Stderr, "sensei-code resume: %v: %s is active in session %s; name that session, or omit "+
				"--session\n", errTaskNotInSession, strings.TrimSpace(*taskID), elsewhere.SessionID)
			return exitUsage
		}
		fmt.Fprintf(os.Stderr, "sensei-code resume: %v: %s\n", errTaskUnknown, strings.TrimSpace(*taskID))
		return exitUsage
	}
	// A task that recorded no objective is refused HERE, ahead of both
	// continuations, because neither of them survives it: an implementation has
	// nothing to implement, and an --answer would spend a human-owned decision
	// re-entering a governed path that refuses an empty objective. The task is
	// still listed -- it exists and its candidate may be on disk -- and this is
	// the sentence that says why it cannot go on.
	if !holder.Task.ObjectiveUsable() {
		fmt.Fprintf(os.Stderr, "sensei-code resume: %v: %s\n", errObjectiveUnusable, holder.Task.TaskID)
		return exitUsage
	}
	// One physical ledger, a fresh actor (70B2a1). The identity every lane
	// below acts under is established HERE, once, before any lane is chosen:
	// the holder's record, and a FRESH session minted for this process
	// (openResumeIdentity). No lane is handed the holder's SessionID; each
	// binds the fresh one to the task's session lineage before it records
	// anything of the task, and the record's historical events keep the
	// sessions they were written under.
	store, sessionID, err := openResumeIdentity(repo.Root, holder.SessionID, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code resume:", err)
		return exitFailed
	}
	interrupted := tasksOf(active)

	// A task named WITHOUT an answer is continued at whatever it currently owes,
	// decided once by selectResumeLane rather than by asking each lane in turn.
	if strings.TrimSpace(*answer) == "" {
		// The durable review obligation is established BEFORE the lane is
		// chosen: a process can die after publishing a review request and
		// recording the obligation but before the workflow writes its
		// WAITING_REVIEW terminal, and that task owes a review the transcript
		// does not mention.
		owed, err := owedReviewObligation(repo.Root, *taskID)
		if err != nil {
			fmt.Fprintln(os.Stderr, "sensei-code resume:", err)
			return exitFailed
		}
		lane, err := selectResumeLane(holder.Task, owed != nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, "sensei-code resume:", err)
			return exitUsage
		}
		switch lane {
		case laneReview:
			return resumeAwaitingReview(ctx, repo, cfg, store, sessionID, interrupted, *taskID, *timeout, *asJSON, *quiet)
		case laneBlocked:
			// The lane is chosen above; this re-reads the block record to check
			// it is bound to this task, and refuses if it is not.
			target, blocked, err := selectBlockedResume(interrupted, *taskID, owed != nil)
			if err != nil {
				fmt.Fprintln(os.Stderr, "sensei-code resume:", err)
				return exitUsage
			}
			if !blocked {
				fmt.Fprintln(os.Stderr, "sensei-code resume: the record no longer states the blocked turn it was "+
					"routed to; nothing is continued")
				return exitFailed
			}
			return resumeBlockedExternal(ctx, repo, cfg, store, sessionID, target, *timeout, *asJSON, *quiet)
		default:
			return resumeInterruptedWork(ctx, repo, cfg, store, sessionID, holder.Task, lane, *timeout, *asJSON, *quiet)
		}
	}

	// Decided from the durable record before anything is started. A refusal here
	// costs nothing and starts nothing.
	target, err := selectAuthorityResume(interrupted, *taskID, *answer)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code resume:", err)
		return exitUsage
	}
	// And an answer this record cannot support is refused here, before a process,
	// a graph or a bridge exists -- so the question is untouched.
	if err := admitLegacyAnswer(target); err != nil {
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
	attempt := engine.ResumeTask(ctx, target.Task)
	return settleResumed(ctx, answered, events, attempt, *asJSON, *quiet, *timeout)
}

// settleResumed is how EVERY lane of this command waits for the task it
// resumed, through that invocation's own control handle (70B2a1, RULING-189).
// First its pre-binding outcome, which the engine settles exactly once: a
// typed refusal is the outcome of this invocation -- reported once, with no
// workflow ending waited for, because none can lawfully be recorded -- and
// only the verified binding of the fresh session lets the run be streamed,
// exactly as a submitted run is (settleInvocation): until it settles, or
// until the engine halts it because the session record did not take one of
// its governed events, whose typed failure is then the outcome. Nothing on
// the bus stands in for either.
func settleResumed(ctx context.Context, control runControl, events <-chan event.Event,
	attempt *workflow.ResumeAttempt, asJSON, quiet bool, timeout time.Duration) int {
	binding := attempt.Binding()
	if binding.Refusal != nil {
		fmt.Fprintln(os.Stderr, "sensei-code resume:", binding.Refusal)
		return exitFailed
	}
	if !quiet {
		fmt.Fprintf(os.Stderr, "sensei-code resume: task %s continues as session %s, bound to its session lineage\n",
			binding.TaskID, binding.CurrentSessionID)
	}
	return settleInvocation(ctx, control, events, attempt.TaskID, attempt, asJSON, quiet, timeout, "sensei-code resume")
}

// reviewState is what the durable review store says about one task, read before
// that task's lane is chosen.
//
// The conflict is carried rather than raised, because a listing and a router
// answer different questions. `--task` names ONE task, so a conflict in its
// records ends the command. `--list` is the map of everything active, and
// refusing the whole map because one task's two records disagree would hide
// every other task -- which is the disappearance this repair exists to end. So
// a conflicted task is RENDERED as unroutable, with the reason, and the rest of
// the listing stands. Fail closed about that task; do not fail closed about the
// repository.
type reviewState struct {
	owed     *ghbridge.ReviewObligation
	conflict error
}

// loadReviewStates reads the durable review obligation of every active task.
//
// A store failure is RETURNED. The listing used to consult no obligation at all
// and route purely from the session transcript, so a task whose process died
// after the review request was published and recorded but before the workflow
// wrote its WAITING_REVIEW terminal was printed as owing implementation while
// `--task` correctly sent it to review. The projection disagreed with the
// router, and the durable owner of review lifetime was the one neither of them
// asked (sensei_code.reviewobligation.a_waiter_is_disposable_the_obligation_is_not).
func loadReviewStates(repoRoot string, active []session.Active) (map[string]reviewState, error) {
	states := map[string]reviewState{}
	for _, entry := range active {
		if _, seen := states[entry.Task.TaskID]; seen || entry.Task.Unavailable != nil {
			// A refused task is shown with its refusal and routed nowhere, so
			// no obligation is read for it -- and none can hide the others.
			continue
		}
		owed, err := owedReviewObligation(repoRoot, entry.Task.TaskID)
		if err != nil {
			return nil, fmt.Errorf("the review obligation of task %s could not be established, so what it owes "+
				"is unknown rather than absent: %w", entry.Task.TaskID, err)
		}
		states[entry.Task.TaskID] = reviewState{owed: owed, conflict: reviewIdentityConflict(entry.Task, owed)}
	}
	return states, nil
}

// printActiveTasks shows every task this repository has begun and not finished,
// and what each of them currently owes.
//
// EVERY active task, not only the ones asking a question or holding a typed
// block. A listing that rendered three of the lanes taught its readers that the
// other two do not exist: a task interrupted before it was planned was absent
// here and refused by --task, so the only visible evidence of it was a candidate
// worktree nobody could account for.
//
// The obligation is rendered through selectResumeLane, the same ordered decision
// --task routes on, AND with the same durable review state --task establishes
// before routing. Passing false for it -- which this used to do, with a comment
// admitting the listing could not see an obligation recorded outside the session
// record -- meant a task could be advertised at a lane resuming would not take.
// What is printed is now what resuming would do.
func printActiveTasks(out io.Writer, scope string, active []session.Active, review map[string]reviewState) {
	standing := 0
	for _, entry := range active {
		task := entry.Task
		fmt.Fprintf(out, "task %s  session %s\n", task.TaskID, entry.SessionID)
		if s := strings.TrimSpace(task.Task); s != "" {
			fmt.Fprintf(out, "  objective  %s\n", s)
		}
		if task.Unavailable != nil {
			// Shown, typed, and routed nowhere: the refusal precedes every
			// other reading of the task, its review state included.
			_, laneErr := selectResumeLane(task, false)
			fmt.Fprintf(out, "  owed       %v\n\n", laneErr)
			continue
		}
		state := review[task.TaskID]
		if state.conflict != nil {
			// Shown, and shown as unroutable. Choosing a lane for it would pick
			// one of two records that disagree about which review this candidate
			// is owed, and the listing has no better claim to that choice than
			// the router, which refuses it.
			fmt.Fprintf(out, "  owed       cannot be decided: %v\n", state.conflict)
			fmt.Fprintln(out)
			continue
		}
		lane, laneErr := selectResumeLane(task, state.owed != nil)
		switch lane {
		case laneUnusable:
			// Discoverable, and plainly not continuable. Dropping it from the
			// listing is what this repair removed; pretending it can be resumed
			// would be the same dishonesty facing the other way.
			fmt.Fprintf(out, "  owed       %v\n", laneErr)
		case laneQuestion:
			standing++
			printStandingQuestion(out, task)
		case laneBlocked:
			printBlockedObligation(out, task)
		default:
			fmt.Fprintf(out, "  owed       %s\n", lane)
			if s := strings.TrimSpace(task.Review); s != "" && lane == laneImplementation {
				fmt.Fprintf(out, "  review     %s\n", oneLine(s))
			}
			fmt.Fprintf(out, "  resume     --task %s\n", task.TaskID)
		}
		fmt.Fprintln(out)
	}
	where := "all sessions"
	if strings.TrimSpace(scope) != "" {
		where = "session " + scope
	}
	if len(active) == 0 {
		fmt.Fprintf(out, "%s: no task is active\n", where)
		return
	}
	if standing == 0 {
		fmt.Fprintf(out, "%s: %d active, no human-owned question is standing\n", where, len(active))
		return
	}
	fmt.Fprintf(out, "%s: %d active, %d standing\n", where, len(active), standing)
}

// printStandingQuestion renders the question a task is asking, and the option
// ids --answer will accept. Without this a person has to read the event log to
// find out what this command takes.
func printStandingQuestion(out io.Writer, task session.Interrupted) {
	var q workflow.DeferredAuthority
	if err := json.Unmarshal(task.AwaitingAuthority, &q); err != nil {
		fmt.Fprintf(out, "  the preserved question could not be read back: %v\n", err)
		return
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
}

// printBlockedObligation renders a turn a provider blocked, or a re-plan a
// non-converged task is owed. The retry time printed is the one the provider
// supplied, or UNKNOWN.
func printBlockedObligation(out io.Writer, task session.Interrupted) {
	if len(task.NotConverged) != 0 {
		if n, err := workflow.ParseNotConverged(task.NotConverged); err != nil {
			fmt.Fprintf(out, "  not converged, but the record could not be read back: %v\n", err)
		} else {
			fmt.Fprintf(out, "  not converged  %s\n  resume         --task %s\n", n.Describe(), task.TaskID)
		}
		return
	}
	block, err := workflow.ParseExternalBlock(task.BlockedExternal)
	if err != nil {
		fmt.Fprintf(out, "  blocked external, but the record could not be read back: %v\n", err)
		return
	}
	fmt.Fprintf(out, "  blocked    %s\n  resume     --task %s\n", block.Describe(), task.TaskID)
}

// tasksOf is the tasks alone, for the selectors that decide from a task record
// and have no use for which session holds it.
func tasksOf(active []session.Active) []session.Interrupted {
	out := make([]session.Interrupted, 0, len(active))
	for _, entry := range active {
		out = append(out, entry.Task)
	}
	return out
}

// activeTask finds one task by the id a person named. It never falls back to a
// near match or to "the only one there is": continuing a task nobody named is
// how a restart silently works on something else.
func activeTask(active []session.Active, taskID string) (session.Active, bool) {
	taskID = strings.TrimSpace(taskID)
	for _, entry := range active {
		if entry.Task.TaskID == taskID {
			return entry, true
		}
	}
	return session.Active{}, false
}

// taskRefusalError is the command's typed refusal for a task the repository's
// discovery refuses (Discovery.TaskRefusal): QUARANTINED for a task a
// quarantined record claims, unavailable for one whose history cannot be read
// as its own session lineage.
func taskRefusalError(sessionID string, refusal *session.TaskHistoryRefusal) error {
	if refusal.Reason == session.TaskHistoryQuarantined {
		return fmt.Errorf("%w: %s is claimed by quarantined session %s (QUARANTINED)", errTaskQuarantined,
			refusal.TaskID, sessionID)
	}
	return fmt.Errorf("%w (session %s): %w", errTaskHistoryUnavailable, sessionID, refusal)
}

// printQuarantinedTasks lists every task a quarantined session record claims:
// reserved, visible and not resumable. Diagnostic only; it grants nothing.
func printQuarantinedTasks(out io.Writer, quarantined []session.QuarantinedRecord) {
	for _, q := range quarantined {
		for _, taskID := range q.Manifest.Claims {
			fmt.Fprintf(out, "task %s  session %s\n  owed       nothing: QUARANTINED -- %v\n\n", taskID, q.SessionID, errTaskQuarantined)
		}
	}
}

// oneLine keeps a multi-line review readable inside a listing.
func oneLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " · "))
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}

// standsAnAnswerableQuestion is the ONE definition of "this task is asking
// something a person can answer", so the listing, its footer and the "look in
// these other sessions" hint cannot disagree about which tasks those are.
//
// An unusable objective disqualifies one. Such a task holds a question nobody
// can answer -- the lane selector refuses it ahead of laneQuestion -- and
// counting it would suppress the hint on the strength of a question this command
// will not accept an answer to, leaving a person with no answerable question and
// no pointer to one.
func standsAnAnswerableQuestion(task session.Interrupted) bool {
	return len(task.AwaitingAuthority) != 0 && task.ObjectiveUsable()
}

// countStanding is how many of these tasks are actually asking something.
func countStanding(tasks []session.Interrupted) int {
	n := 0
	for _, task := range tasks {
		if standsAnAnswerableQuestion(task) {
			n++
		}
	}
	return n
}

// reportOtherSessionsWithQuestions points at sessions that DO hold a standing
// question, so an empty scoped listing is not read as "there are none".
//
// IT IS A PROJECTION OF THE VALIDATED INVENTORY, not a second walk of storage.
// It used to read the sessions directory itself and skip every failure it met --
// an unlistable directory, a record it could not open, a history it could not
// parse -- returning silently in each case. So the one sentence whose whole
// purpose is to say "look elsewhere" said nothing at all about exactly the
// records that might be hiding the question, and an empty listing beside that
// silence read as a repository with nothing standing in it. Discovery has
// already refused those records outright by the time this is called; what
// remains is a grouping of what it returned.
//
// It decides nothing and resumes nothing. Naming a session is the person's act,
// which is why --session exists rather than this picking one.
func reportOtherSessionsWithQuestions(out io.Writer, inventory []session.Active, skip string) {
	standing := map[string]int{}
	for _, entry := range inventory {
		if entry.SessionID == skip || !standsAnAnswerableQuestion(entry.Task) {
			continue
		}
		standing[entry.SessionID]++
	}
	var others []string
	for sessionID, n := range standing {
		others = append(others, fmt.Sprintf("  --session %s   (%d standing)", sessionID, n))
	}
	if len(others) == 0 {
		return
	}
	sort.Strings(others)
	fmt.Fprintf(out, "\nOther sessions hold standing questions:\n%s\n", strings.Join(others, "\n"))
}

// admitLegacyAnswer refuses, before anything starts, an answer that would
// authorize work on a question whose record cannot prove what it was asked about.
//
// The engine refuses this too, at the rendezvous, and that is the enforcement
// that matters -- the TUI and any other surface go through it. This refuses
// EARLIER, so the refusal costs no process, no Sensei, no graph and no
// bridge: a person who names an inadmissible answer is told so instead of
// watching a run start and end.
//
// The 2026-09-13 incident is why this is a refusal and not the warning it was:
// the warning printed, execution continued, and an authorization was recorded in
// the owner's name. There is deliberately no flag to restore that.
//
// A stop is admitted because it authorizes no subsequent work. It ends the task
// rather than permitting a change, so there is nothing for coverage to govern.
func admitLegacyAnswer(target authorityResume) error {
	if target.Question.ScopeRecorded || target.Option.Outcome == authority.Stop {
		return nil
	}
	return fmt.Errorf("%w: answer %s (%s) would authorize work on a question deferred before its authority scope was "+
		"preserved, and the historical scope must not be reconstructed from the repository as it stands now; "+
		"the question is left standing and only the stop option is admissible for this record",
		errUnprovableScope, target.Option.ID, target.Option.Outcome)
}

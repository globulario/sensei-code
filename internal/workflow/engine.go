package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/authority"
	"github.com/globulario/sensei-code/internal/behavioral"
	"github.com/globulario/sensei-code/internal/broker"
	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/decision"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/provider"
	"github.com/globulario/sensei-code/internal/publish"
	"github.com/globulario/sensei-code/internal/report"
	"github.com/globulario/sensei-code/internal/sensei"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/taskstate"
	"github.com/globulario/sensei-code/internal/validation"
)

type Engine struct {
	Repo      gitx.Repo
	Config    config.Config
	Bus       *event.Bus
	Store     *session.Store
	SessionID string

	mu      sync.Mutex
	pending map[string]chan string
	// notes holds guidance the human typed while a task was running, keyed by
	// task. It is a queue rather than an interrupt: a worker mid-cycle cannot be
	// spoken to, so the guidance waits for the next boundary where it can
	// actually be read.
	notes map[string][]string
}

func New(repo gitx.Repo, cfg config.Config, bus *event.Bus, store *session.Store, sessionID string) *Engine {
	return &Engine{Repo: repo, Config: cfg, Bus: bus, Store: store, SessionID: sessionID, pending: make(map[string]chan string)}
}

// RotateSession starts a fresh session log, abandoning the previous
// conversation without deleting its record. It is only safe between tasks; the
// TUI calls it for /clear, which it refuses while a task is running.
func (e *Engine) RotateSession() error {
	id := session.ID(time.Now())
	store, err := session.New(e.Repo.Root, id)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.SessionID = id
	e.Store = store
	return nil
}

func (e *Engine) emit(ev event.Event) {
	if e.Store != nil {
		_ = e.Store.Append(ev)
	}
	if e.Bus != nil {
		e.Bus.Publish(ev)
	}
}

// Errors shared by both workflows, named so the two state machines refuse in
// the same words rather than drifting apart.
var (
	errEmptyTask        = errors.New("task is empty")
	errNoReadCapability = errors.New("repository read capability is not granted")
	errArchitectSilent  = errors.New("the architect returned nothing")
)

// Note queues guidance for a running task. It reports whether the task could
// accept it, so the caller never tells the human their message was taken when
// nothing will read it.
func (e *Engine) Note(taskID, text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || strings.TrimSpace(taskID) == "" {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.notes == nil {
		e.notes = map[string][]string{}
	}
	e.notes[taskID] = append(e.notes[taskID], text)
	return true
}

// takeNotes removes and returns the queued guidance for a task.
func (e *Engine) takeNotes(taskID string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	notes := e.notes[taskID]
	delete(e.notes, taskID)
	return notes
}

// ResolveHuman resumes an exact task waiting at a Level-3 authority boundary.
// It returns false if that task is not currently awaiting a human decision.
func (e *Engine) ResolveHuman(taskID, optionID string) bool {
	e.mu.Lock()
	ch, ok := e.pending[taskID]
	e.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- optionID:
		return true
	default:
		return false
	}
}

// conversationSoFar reconstructs the dialogue with the architect from the
// session record. The architect process is started fresh for every turn, so
// without this it has no memory and reintroduces itself on every message.
// The current task is excluded: it is quoted separately as the live request.
func (e *Engine) conversationSoFar(current string, limit int) string {
	if e.Store == nil {
		return ""
	}
	events, err := e.Store.Load()
	if err != nil {
		return ""
	}
	var turns []string
	for _, ev := range events {
		text := strings.TrimSpace(ev.Summary)
		if text == "" {
			continue
		}
		switch {
		case ev.Kind == event.TaskCreated:
			turns = append(turns, "HUMAN: "+text)
		case ev.Kind == event.ArchitectSpoke:
			turns = append(turns, "YOU: "+text)
		case ev.Source == event.SourceArchitect && ev.Kind == event.Status:
			turns = append(turns, "YOU (decision): "+text)
		}
	}
	// run() records the live request before reaching the architect.
	if n := len(turns); n > 0 && turns[n-1] == "HUMAN: "+strings.TrimSpace(current) {
		turns = turns[:n-1]
	}
	if len(turns) > limit {
		turns = turns[len(turns)-limit:]
	}
	return strings.Join(turns, "\n")
}

// taskContext is everything the architect knew when it decided. It is carried
// to every other role so a worker and a reviewer judge the same request the
// architect judged, instead of receiving a plan stripped of the reasoning that
// produced it.
type taskContext struct {
	Task            string
	Conversation    string
	WorkspaceStatus string
	Preflight       string
	Rationale       string
	Steps           []string
	Domain          string
	Consequences    string
	Invariants      []string
	// Report is the rendered change report, set once the candidate is judged so
	// the pull request body carries the same evidence the architect saw.
	Report string
	// Identity binds this task to the exact base it governs.
	Identity candidate.Identity
	// EvidenceSnapshot is what the candidate contained at the last audit, kept
	// so a handover states the position rather than describing it.
	EvidenceSnapshot taskstate.Evidence
}

// intent renders the architect's stated reasoning for the roles downstream.
func (c taskContext) intent() string {
	var b strings.Builder
	if r := strings.TrimSpace(c.Rationale); r != "" {
		b.WriteString(r)
		b.WriteString("\n")
	}
	for i, step := range c.Steps {
		b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, strings.TrimSpace(step)))
	}
	if b.Len() == 0 {
		return "(the architect recorded no additional rationale)"
	}
	return strings.TrimRight(b.String(), "\n")
}

func (e *Engine) run(ctx context.Context, taskID, task string) {
	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.TaskCreated, task, nil))
	e.announceMode(taskID, governedMode(RequestedByHuman))
	fail := func(err error) {
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.WorkflowFailed, err.Error(), nil))
		e.reportOutcome(ctx, "failure", task, err.Error())
	}
	if task == "" {
		fail(errEmptyTask)
		return
	}
	if !e.Config.Permissions.ReadRepository {
		fail(errNoReadCapability)
		return
	}

	sc, err := sensei.Start(ctx, e.Repo.Root, e.Config.Sensei.Command, e.Config.Sensei.Args)
	if err != nil {
		fail(fmt.Errorf("start Sensei: %w", err))
		return
	}
	defer sc.Close()

	workspaceStatus, err := sc.CallTool("sensei_workspace_status", map[string]any{"repo": e.Repo.Root})
	if err != nil {
		fail(fmt.Errorf("Sensei workspace status: %w", err))
		return
	}
	e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.SenseiResult, firstText(workspaceStatus), workspaceStatus.Structured))

	preflightArgs := map[string]any{"task": task, "files": []string{}, "mode": "compact"}
	// Scope preflight to the domain Sensei just stated in the workspace identity
	// receipt. Left unscoped against a graph hosting more than one domain,
	// Sensei cannot resolve the repository and answers UNKNOWN_IMPACT with a
	// domain_scope blind spot, which is not usable architectural evidence.
	if domain := sensei.RepositoryDomain(workspaceStatus); domain != "" {
		preflightArgs["domain"] = domain
	}
	preflight, err := sc.CallTool("awareness_preflight", preflightArgs)
	if err != nil {
		fail(fmt.Errorf("Sensei preflight: %w", err))
		return
	}
	e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.SenseiResult, firstText(preflight), preflight.Structured))

	// The start gate runs before the architect is consulted. Its refusal is not
	// overridable by the architect's decision, because an architect handed a
	// stale or uncertifiable graph produces a confident, specific plan built on
	// invariants that no longer hold — which reads as excellent work.
	start, err := certifyStart(workspaceStatus, preflight)
	if err != nil {
		e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status, err.Error(), preflight.Structured))
		e.reportOutcome(ctx, "blocked", task, err.Error())
		fail(err)
		return
	}

	conversation := e.conversationSoFar(task, 40)
	decision, err := e.resolveArchitecture(ctx, sc, start, taskID, task, architecturePrompt(e.Repo.Root, sensei.RepositoryDomain(workspaceStatus), config.DisplayName(e.Config.Architect.Name), task, conversation, firstText(workspaceStatus), firstText(preflight)))
	if err != nil {
		fail(err)
		return
	}
	if decision.Decision == "reply" {
		e.emit(event.New(e.SessionID, taskID, event.SourceArchitect, event.ArchitectSpoke, decision.Message, decision))
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.WorkflowCompleted, "", nil))
		return
	}
	e.emit(event.New(e.SessionID, taskID, event.SourceArchitect, event.PlanProposed, planSummary(decision), decision))
	approved, err := e.approvePlan(ctx, taskID, decision)
	if err != nil {
		fail(err)
		return
	}
	if !approved {
		e.reportOutcome(ctx, "blocked", task, "the human declined the proposed plan")
		e.emit(event.New(e.SessionID, taskID, event.SourceArchitect, event.ArchitectSpoke,
			"Holding off. Nothing has been implemented -- tell me what to change about the plan.", nil))
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.WorkflowCompleted, "", nil))
		return
	}
	plan := decision.Plan
	tc := taskContext{
		Task:            task,
		Conversation:    conversation,
		WorkspaceStatus: firstText(workspaceStatus),
		Preflight:       firstText(preflight),
		Rationale:       decision.Summary,
		Steps:           decision.Steps,
		Domain:          sensei.RepositoryDomain(workspaceStatus),
		Consequences:    decision.Consequences,
		Invariants:      decision.Invariants,
	}

	if !e.Config.Permissions.CreateWorktrees || !e.Config.Permissions.WriteCandidates {
		fail(errors.New("candidate worktree capability is not granted"))
		return
	}
	if len(e.Config.Implementors) == 0 {
		fail(errors.New("no implementor is configured"))
		return
	}

	e.implement(ctx, sc, start, taskID, &tc, plan, "", fail)
}

// approvePlan shows the architect's bounded plan and waits for the human to
// accept it before any worker touches a candidate. The architect may decide
// ordinary architectural questions on its own, but committing the human's
// repository to a body of work is theirs to start.
func (e *Engine) approvePlan(ctx context.Context, taskID string, d architectureDecision) (bool, error) {
	options := []authority.Option{
		{ID: "1", Label: "Implement this plan", Description: "hand it to the bounded workers"},
		{ID: "2", Label: "Not yet, let us talk about it", Description: "nothing is implemented"},
	}
	decision := authority.Decision{
		Level:          authority.Human,
		Subject:        "Implement this plan?",
		Reason:         d.Summary,
		Recommendation: "1",
		Options:        options,
	}
	choice, err := e.awaitChoice(ctx, nil, taskID, "", "", "", decision, options)
	if err != nil {
		return false, err
	}
	return strings.HasPrefix(choice, "1:"), nil
}

// offerPullRequest asks whether to publish an accepted candidate. Publication
// is human-owned, so it is gated twice: the configuration must grant push at
// all, and the human must say yes to this particular change. Sensei Code opens
// the pull request and stops there; merging is never its decision.
func (e *Engine) offerPullRequest(ctx context.Context, taskID string, tc *taskContext, workspace, worker string) {
	if !e.Config.Permissions.Push {
		e.emit(event.New(e.SessionID, taskID, event.SourceGit, event.Status,
			"no pull request offered: "+publish.ErrPushNotGranted.Error(), nil))
		return
	}
	options := []authority.Option{
		{ID: "1", Label: "Open a pull request", Description: "pushes the candidate branch; it is not merged"},
		{ID: "2", Label: "Leave it local", Description: "the candidate stays in its worktree"},
	}
	reason := "Sensei Code can publish the branch. It cannot merge it, and a pull request is not an admission."
	// The behavioral gate informs this decision; it never replaces it. Its
	// answer is shown to the human rather than acted on, because an ungoverned
	// "allowed" means no principle covered the action, and a tool that read only
	// the status would turn the absence of a rule into a permission.
	if verdict := e.behavioralVerdict(ctx, "open a pull request from an AI-generated candidate", tc.Domain); verdict != "" {
		reason += "\n\n" + verdict
	}
	choice, err := e.awaitChoice(ctx, nil, taskID, "", "", "", authority.Decision{
		Level:          authority.Human,
		Subject:        "Open a pull request for this candidate?",
		Reason:         reason,
		Recommendation: "1",
		Options:        options,
	}, options)
	if err != nil || !strings.HasPrefix(choice, "1:") {
		return
	}
	url, err := publish.Open(ctx, publish.Request{
		Workspace: workspace,
		Branch:    e.Repo.WorktreeBranch(taskID),
		Base:      e.Config.Workflow.PublishBase,
		Title:     tc.Task,
		Report:    tc.Report,
	}, e.Config.Permissions.Push, e.Config.Permissions.LocalCommit)
	if err != nil {
		e.emit(event.New(e.SessionID, taskID, event.SourceGit, event.WorkflowFailed,
			"pull request not opened: "+err.Error(), nil))
		return
	}
	e.emit(event.New(e.SessionID, taskID, event.SourceGit, event.PullRequestOpened,
		"pull request opened, not merged and not admitted: "+url, map[string]string{"url": url}))
}

// reportUndeliveredNotes says so when the human typed guidance that no worker
// cycle ever read. Silently discarding it would leave the architect believing
// they had steered a run they did not touch.
func (e *Engine) reportUndeliveredNotes(taskID string) {
	notes := e.takeNotes(taskID)
	if len(notes) == 0 {
		return
	}
	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status,
		fmt.Sprintf("the task finished before your guidance was read, so it was not delivered: %s",
			strings.Join(notes, " / ")), nil))
}

// emitChangeReport tells the architect what the candidate actually changed.
// Every figure is counted from the diff or quoted from Sensei; nothing is the
// worker's account of its own work.
func (e *Engine) emitChangeReport(ctx context.Context, sc *sensei.Client, taskID string, tc *taskContext, diff, audit string) string {
	change := report.FromDiff(diff)
	change.Audit = audit

	// Ask Sensei what governs the files the candidate actually touched, rather
	// than reusing the preflight taken before anyone knew which files those
	// would be.
	paths := make([]string, 0, len(change.Files))
	for _, f := range change.Files {
		if f.Status != report.Deleted {
			paths = append(paths, f.Path)
		}
	}
	if len(paths) != 0 {
		args := map[string]any{"task": tc.Task, "files": paths, "mode": "compact"}
		if tc.Domain != "" {
			args["domain"] = tc.Domain
		}
		if result, err := sc.CallTool("awareness_preflight", args); err == nil {
			change.Risk = structuredString(result.Structured, "risk_class")
			change.Governing = governingInvariants(result.Structured)
		}
	}
	e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.ChangeReported, change.Render(tc.Task), change))
	return change.Render(tc.Task)
}

// governingInvariants pulls invariant titles out of preflight's required
// actions. Sensei states them; they are not derived here.
func governingInvariants(structured map[string]any) []string {
	actions, _ := structured["required_actions"].([]any)
	var out []string
	for _, action := range actions {
		text, _ := action.(string)
		if rest, ok := strings.CutPrefix(text, "Verify invariant still holds: "); ok {
			out = append(out, rest)
		}
	}
	return out
}

func structuredString(structured map[string]any, key string) string {
	value, _ := structured[key].(string)
	return value
}

// behavioralVerdict asks the behavioral gate about an action and returns the
// line a human should read before deciding. It returns "" when no gate is
// configured, and reports a gate that could not answer rather than treating
// silence as approval.
func (e *Engine) behavioralVerdict(ctx context.Context, action, target string) string {
	decision, err := behavioral.New(e.Config.Behavioral).CheckAction(ctx, action, target)
	switch {
	case errors.Is(err, behavioral.ErrNotConfigured):
		return ""
	case err != nil:
		return "behavioral gate: could not be reached, so it gave no answer (" + err.Error() + ")"
	}
	return decision.Summary()
}

// reportOutcome files what became of a task with the behavioral service, so
// principles are learned from real runs. It is deliberately fire-and-forget:
// the workflow's own result is what happened, whether or not the fact could be
// filed, and a reporting failure must never turn a successful task into a
// failed one.
func (e *Engine) reportOutcome(ctx context.Context, status, task, note string) {
	client := behavioral.New(e.Config.Behavioral)
	if err := client.Record(ctx, behavioral.Outcome{
		Status: status,
		Theme:  "sensei_code.candidate_workflow",
		Note:   strings.TrimSpace(task + " -- " + note),
	}); err != nil && !errors.Is(err, behavioral.ErrNotConfigured) {
		e.emit(event.New(e.SessionID, "", event.SourceSystem, event.Status,
			"behavioral outcome not recorded: "+err.Error(), nil))
	}
}

// recordDecision writes the accepted plan into Sensei as an architectural
// decision, so the reason this work was authorized outlives the session and any
// agent can read it later. A decision Sensei would refuse is reported, never
// padded with invented links to make it pass.
func (e *Engine) recordDecision(ctx context.Context, taskID string, tc *taskContext, changed []string) {
	record := decision.Record{
		Title:        strings.TrimSpace(tc.Rationale),
		Rationale:    tc.Task,
		Context:      "Accepted by the human in an interactive Sensei Code session.",
		Consequences: tc.Consequences,
		SourceFiles:  changed,
		Invariants:   tc.Invariants,
		Repo:         tc.Domain,
		Domain:       tc.Domain,
		RepoRoot:     e.Repo.Root,
	}
	if strings.TrimSpace(record.Title) == "" {
		record.Title = tc.Task
	}
	err := decision.Write(ctx, record)
	if err == nil {
		e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.DecisionRecorded,
			"architectural decision recorded for review: "+record.Title, nil))
		return
	}
	// Not recording is a gap in the shared memory, so it is said out loud
	// rather than swallowed.
	e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.DecisionRecorded,
		"decision not recorded: "+err.Error(), nil))
}

// changedPaths lists the files a candidate actually touched, so a decision
// references work that exists rather than work that was intended.
func changedPaths(diff string) []string {
	var out []string
	for _, change := range report.FromDiff(diff).Files {
		if change.Status != report.Deleted {
			out = append(out, change.Path)
		}
	}
	return out
}

// planSummary renders the plan as something an architect can read and judge:
// the decision, then the concrete work it commits to.
func planSummary(d architectureDecision) string {
	var b strings.Builder
	if summary := strings.TrimSpace(d.Summary); summary != "" {
		b.WriteString(summary)
		b.WriteString("\n")
	}
	if len(d.Steps) != 0 {
		b.WriteString("\nPlan:\n")
		for i, step := range d.Steps {
			b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, strings.TrimSpace(step)))
		}
	} else if plan := strings.TrimSpace(d.Plan); plan != "" {
		b.WriteString("\nPlan:\n  ")
		b.WriteString(strings.ReplaceAll(plan, "\n", "\n  "))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// tc is a pointer because the change report is produced here and read by the
// caller when it offers publication. Taking it by value silently dropped the
// report, and the pull request body went out with the evidence missing.
func (e *Engine) runCandidate(ctx context.Context, sc *sensei.Client, start certifiedStart, taskID string, tc *taskContext, initialPlan string, worker config.Agent, workspace, carried string) (bool, string, string, string, error) {
	task := tc.Task
	plan := initialPlan
	// A handover from the previous worker is review feedback that has not been
	// answered yet, so it enters this worker's first cycle as exactly that.
	feedback := carried
	var lastReview, lastAudit string
	// previousDiffDigest detects a worker that is not responding to feedback.
	var previousDiffDigest string

	// Candidate isolation is the blast-radius boundary, so it is checked rather
	// than assumed: the workspace is a string threaded through several call
	// sites, and one that accidentally holds the canonical root looks like
	// nothing at all until a worker has edited the human's own files.
	if err := broker.GuardCanonicalCheckout(e.Repo.Root, workspace); err != nil {
		return false, plan, lastReview, lastAudit, err
	}

	// The declared capability envelope is realised here rather than described
	// in a prompt. This runs unconditionally: force_push needs its own hook
	// even when push itself is granted, which the previous push-only condition
	// could not express.
	envelope := broker.New(e.Config.Permissions)
	guardEnv, err := envelope.Enforce(broker.GuardDir(e.Repo.Root, taskID), workspace)
	if err != nil {
		return false, plan, lastReview, lastAudit, fmt.Errorf("install the capability guard: %w", err)
	}
	if gaps := envelope.Unenforceable(); len(gaps) != 0 {
		// Denied, but not mechanically preventable. Said out loud so the
		// transcript never implies a boundary the runtime does not have.
		names := make([]string, 0, len(gaps))
		for _, g := range gaps {
			names = append(names, string(g))
		}
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status,
			"declared but not mechanically enforced: "+strings.Join(names, ", "), nil))
	}

	for cycle := 1; cycle <= e.Config.Workflow.ReviewCycles; cycle++ {
		guidance := e.takeNotes(taskID)
		if len(guidance) != 0 {
			e.emit(event.New(e.SessionID, taskID, event.SourceUser, event.GuidanceDelivered,
				strings.Join(guidance, "\n"), nil))
		}
		prompt := implementationPrompt(*tc, plan, feedback, cycle, guidance)
		impl := agent.CLI{Name: worker.Name, Label: config.DisplayName(worker.Name), Command: worker.Command, Args: worker.Args, Source: sourceFor(worker.Name), SessionID: e.SessionID, Env: guardEnv, UnsetEnv: provider.SessionOnlyEnv}
		if _, err := impl.Run(ctx, agent.Request{Role: agent.Implementor, TaskID: taskID, Workspace: workspace, Prompt: prompt}, e.emit); err != nil {
			return false, plan, lastReview, lastAudit, fmt.Errorf("implementor cycle %d: %w", cycle, err)
		}

		candidate := gitx.Repo{Root: workspace}
		diff, err := candidate.CandidateDiff(ctx, tc.Identity.BaseSHA)
		if err != nil {
			return false, plan, lastReview, lastAudit, err
		}
		if strings.TrimSpace(diff) == "" {
			return false, plan, lastReview, lastAudit, errors.New("implementor produced no candidate diff")
		}
		e.emit(event.New(e.SessionID, taskID, event.SourceGit, event.CandidateChanged, fmt.Sprintf("candidate diff %d bytes · cycle %d", len(diff), cycle), nil))

		// The missing edge: worker → change → validation → typed evidence →
		// reviewer. Without it the reviewer correctly refuses to accept on
		// unproven work and the worker has no way to supply the proof, which
		// deadlocks the loop rather than failing it.
		//
		// Formatters run first because they can rewrite the candidate, and
		// everything gathered before a rewrite is evidence about different
		// bytes. The diff is therefore re-read afterwards and the certifying
		// checks are bound to the digest of what will actually be reviewed.
		evidence, diff, err := e.validate(ctx, taskID, tc.Identity.BaseSHA, envelope, candidate, diff)
		if err != nil {
			return false, plan, lastReview, lastAudit, err
		}
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.ValidationRun, evidence.Render(), evidence))

		auditArgs := map[string]any{"diff": diff, "task": task}
		// Scope the audit to the domain the start gate certified, so the audit
		// evaluates this candidate against this repository's rules rather than
		// whatever the graph resolves by default.
		if domain := start.Domain(); domain != "" {
			auditArgs["domain"] = domain
		}
		// Bind the audit to the exact base this candidate was cut from, so its
		// verdict names the pair it judged rather than an implied HEAD.
		if base := tc.Identity.BaseSHA; base != "" {
			auditArgs["expected_head"] = base
		}
		audit, err := sc.CallTool("awareness_audit_diff", auditArgs)
		if err != nil {
			return false, plan, lastReview, lastAudit, fmt.Errorf("Sensei diff audit: %w", err)
		}
		lastAudit = firstText(audit)
		e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.CandidateAudited, lastAudit, audit.Structured))

		// The audit is decoded before the reviewer is consulted. The reviewer
		// still receives the prose, because prose is what a model reasons over,
		// but the verdict that governs acceptance is the structured one.
		verdict, err := sensei.DecodeDiffAudit(audit)
		if err != nil {
			return false, plan, lastReview, lastAudit, err
		}
		// Surface why an audit did not pass at the moment it happens, rather
		// than only if a reviewer later tries to accept over it. The
		// limitations are where Sensei explains itself, and four acceptance
		// runs were spent guessing at a cause that was sitting unread in that
		// field.
		if !verdict.ReviewerMayAccept() {
			e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status,
				"Sensei audit did not clear this candidate: "+verdict.Diagnostic(), audit.Structured))
		}
		if note := sensei.Discrepancy("diff audit", lastAudit, string(verdict.Decision), sensei.AuditDecisionTokens()); note != "" {
			e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status, note, audit.Structured))
		}
		// A candidate that is byte-identical to the previous cycle means the
		// worker read the feedback and produced the same thing again. Running
		// the remaining cycles will produce it a third and fourth time: one
		// real run did exactly that, three times, before timing out with no
		// diagnosis. Stop and say so, so the next worker gets the candidate
		// while there is still budget to do something with it.
		if digest := strings.TrimSpace(verdict.InputDiffDigest); digest != "" {
			if digest == previousDiffDigest {
				return false, plan, lastReview, lastAudit, fmt.Errorf(
					"the candidate did not change between review cycles: %s produced an identical diff after being asked to revise. "+
						"The last review asked for: %s", config.DisplayName(worker.Name), oneLine(lastReview))
			}
			previousDiffDigest = digest
		}

		// Snapshot the position so a handover states what the candidate holds
		// rather than describing it in prose the next worker has to re-derive.
		tc.EvidenceSnapshot = taskstate.Evidence{
			DiffBytes:     len(diff),
			ChangedPaths:  changedPaths(diff),
			AuditVerdict:  string(verdict.Decision),
			AuditDetail:   verdict.Diagnostic(),
			RequiredTests: tc.EvidenceSnapshot.RequiredTests,
		}

		review, err := e.resolveReview(ctx, taskID, reviewPrompt(*tc, plan, diff, lastAudit, evidence.Render()))
		if err != nil {
			return false, plan, lastReview, lastAudit, err
		}
		lastReview = review.Summary
		switch review.Decision {
		case "accept":
			// Sensei owns this transition. A reviewer that accepts over a
			// refusal does not conclude the candidate; the refusal becomes the
			// next revision instruction instead.
			if judged := judgeCandidate(review.Decision, verdict); !judged.Accepted {
				e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status,
					"reviewer accepted but Sensei refused; the refusal governs: "+judged.Refusal, audit.Structured))
				// A refusal the worker cannot act on must stop the loop rather
				// than drive it. An audit that could not run objects to the
				// environment, not to the candidate, so sending it back produces
				// a byte-identical diff and consumes the next cycle for nothing.
				if !verdict.Actionable() {
					return false, plan, lastReview, lastAudit, fmt.Errorf(
						"Sensei could not verify this candidate and no edit to it would change that: %s", judged.Refusal)
				}
				feedback = reviseInstruction(judged)
				continue
			}
			tc.Report = e.emitChangeReport(ctx, sc, taskID, tc, diff, lastAudit)
			// The decision is recorded now rather than at approval. At approval
			// the architect can only name files it intends to create, and a
			// decision that references a file the task never produced is a
			// reference to nothing.
			e.recordDecision(ctx, taskID, tc, changedPaths(diff))
			return true, plan, lastReview, lastAudit, nil
		case "revise":
			feedback = review.Instructions
			if strings.TrimSpace(feedback) == "" {
				feedback = review.Summary
			}
			e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status, "review requested bounded revision; continuing autonomously", map[string]int{"cycle": cycle}))
		case "escalate":
			revised, err := e.resolveArchitecture(ctx, sc, start, taskID, task, escalationPrompt(task, plan, lastAudit, review))
			if err != nil {
				return false, plan, lastReview, lastAudit, err
			}
			if strings.TrimSpace(revised.Plan) == "" {
				return false, plan, lastReview, lastAudit, errors.New("architect did not return a revised bounded plan")
			}
			e.emit(event.New(e.SessionID, taskID, event.SourceArchitect, event.Status, revised.Summary, revised))
			plan = revised.Plan
			feedback = "The architect resolved the review escalation. Reconcile the current candidate with the revised plan."
		default:
			return false, plan, lastReview, lastAudit, fmt.Errorf("unsupported review decision %q", review.Decision)
		}
	}
	return false, plan, lastReview, lastAudit, fmt.Errorf("candidate did not converge after %d review cycles", e.Config.Workflow.ReviewCycles)
}

type architectureDecision struct {
	Decision       string             `json:"decision"`
	Summary        string             `json:"summary"`
	Message        string             `json:"message,omitempty"`
	Steps          []string           `json:"steps,omitempty"`
	Consequences   string             `json:"consequences,omitempty"`
	Files          []string           `json:"files,omitempty"`
	Invariants     []string           `json:"related_invariants,omitempty"`
	Plan           string             `json:"plan"`
	HumanQuestion  string             `json:"human_question,omitempty"`
	Recommendation string             `json:"recommendation,omitempty"`
	Options        []authority.Option `json:"options,omitempty"`
	// Claims are the factual premises the plan rests on. They are evidence the
	// router reads, not a verdict: a premise the architect marks "inference" is
	// the model telling us nothing checked it.
	Claims []Claim `json:"claims,omitempty"`
}

type reviewDecision struct {
	Decision     string `json:"decision"`
	Summary      string `json:"summary"`
	Instructions string `json:"instructions,omitempty"`
}

func (e *Engine) resolveArchitecture(ctx context.Context, sc *sensei.Client, start certifiedStart, taskID, task, prompt string) (architectureDecision, error) {
	architect := agent.CLI{Name: e.Config.Architect.Name, Label: config.DisplayName(e.Config.Architect.Name), Command: e.Config.Architect.Command, Args: e.Config.Architect.Args, Source: event.SourceArchitect, SessionID: e.SessionID, UnsetEnv: provider.SessionOnlyEnv}
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		p := prompt
		if attempt > 1 {
			p += "\n\nYour previous response was not valid bounded JSON. Return ONLY the required JSON object."
		}
		result, err := architect.Run(ctx, agent.Request{Role: agent.Architect, TaskID: taskID, Workspace: e.Repo.Root, Prompt: p}, e.emit)
		if err != nil {
			lastErr = err
			continue
		}
		var d architectureDecision
		if err := decodeModelJSON(result.Text, &d); err != nil {
			lastErr = err
			continue
		}
		d.Decision = strings.ToLower(strings.TrimSpace(d.Decision))
		switch d.Decision {
		case "reply":
			// The human asked something rather than requesting a change. The
			// architect answers and the governed candidate pipeline never
			// starts: there is nothing to implement, admit, or verify.
			if strings.TrimSpace(d.Message) == "" {
				lastErr = errors.New("architect returned REPLY without a message")
				continue
			}
			return d, nil
		case "proceed":
			if strings.TrimSpace(d.Plan) == "" {
				lastErr = errors.New("architect returned PROCEED without a plan")
				continue
			}
			// The architect proposes; it does not decide whether the proposal
			// carries architectural authority. A confident model proceeding
			// through a region the graph cannot cover is the exact failure this
			// routing exists to catch, and it is invisible at the time because
			// the model sounds no different than usual.
			routing, err := e.routePlan(sc, start, task, d)
			if err != nil {
				return architectureDecision{}, err
			}
			switch {
			case routing.Route == RouteCannotEstablish:
				return architectureDecision{}, fmt.Errorf("cannot establish authority for this plan: %s", routing.Condition)
			case routing.RequiresHuman():
				// Authorizing does not change the graph, so the router will
				// reach this same condition on the next plan. Ask once per
				// condition per task and then honour the answer, or the human
				// is interrogated in a loop and the run never starts.
				if authorized, asked := e.applyAnsweredCondition(taskID, routing.Condition); asked {
					if !authorized {
						return architectureDecision{}, fmt.Errorf(
							"the human declined this architectural change and the plan still requires it: %s", routing.Condition)
					}
					e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status,
						"proceeding on the human's earlier authorization for: "+routing.Condition, nil))
					return d, nil
				}
				e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status, escalationCondition(routing), nil))
				choice, err := e.awaitHuman(ctx, sc, start, taskID, d, routing.Condition)
				if err != nil {
					return architectureDecision{}, err
				}
				prompt = humanResolutionPrompt(prompt, d, choice)
				attempt = 0
				continue
			}
			// No status line here: the caller renders this decision, either as a
			// plan awaiting approval or as a revised plan during review. Emitting
			// the summary as well printed it twice.
			return d, nil
		case "escalate":
			// A model asking to escalate is asking for more investigation. It
			// does not by itself create a human interruption: if Sensei can
			// certify the region, the certification is handed back and the
			// architect decides architecturally. A nervous model must not be
			// able to manufacture Level-3 events, for the same reason a
			// confident one must not be able to skip them.
			routing, err := e.routePlan(sc, start, task, d)
			if err != nil {
				return architectureDecision{}, err
			}
			if routing.Granted() {
				e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status,
					"architect asked to escalate; Sensei certifies this region, so it is resolved architecturally", nil))
				prompt = certifiedResolutionPrompt(prompt, d)
				attempt = 0
				continue
			}
			if routing.Route == RouteCannotEstablish {
				return architectureDecision{}, fmt.Errorf("cannot establish authority for this question: %s", routing.Condition)
			}
			if authorized, asked := e.applyAnsweredCondition(taskID, routing.Condition); asked {
				if !authorized {
					return architectureDecision{}, fmt.Errorf(
						"the human declined this and the architect returned to it: %s", routing.Condition)
				}
				prompt = certifiedResolutionPrompt(prompt, d)
				attempt = 0
				continue
			}
			e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status, escalationCondition(routing), nil))
			choice, err := e.awaitHuman(ctx, sc, start, taskID, d, routing.Condition)
			if err != nil {
				return architectureDecision{}, err
			}
			prompt = humanResolutionPrompt(prompt, d, choice)
			attempt = 0 // the human answer establishes a new architectural question.
			continue
		default:
			lastErr = fmt.Errorf("architect decision must be reply, proceed, or escalate, got %q", d.Decision)
		}
	}
	return architectureDecision{}, fmt.Errorf("architect could not produce a bounded decision: %w", lastErr)
}

func (e *Engine) resolveReview(ctx context.Context, taskID, prompt string) (reviewDecision, error) {
	reviewer := agent.CLI{Name: e.Config.Reviewer.Name, Label: config.DisplayName(e.Config.Reviewer.Name), Command: e.Config.Reviewer.Command, Args: e.Config.Reviewer.Args, Source: event.SourceReviewer, SessionID: e.SessionID, UnsetEnv: provider.SessionOnlyEnv}
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		p := prompt
		if attempt > 1 {
			p += "\n\nReturn ONLY the required JSON object."
		}
		result, err := reviewer.Run(ctx, agent.Request{Role: agent.Reviewer, TaskID: taskID, Workspace: e.Repo.Root, Prompt: p}, e.emit)
		if err != nil {
			lastErr = err
			continue
		}
		var d reviewDecision
		if err := decodeModelJSON(result.Text, &d); err != nil {
			lastErr = err
			continue
		}
		d.Decision = strings.ToLower(strings.TrimSpace(d.Decision))
		if d.Decision != "accept" && d.Decision != "revise" && d.Decision != "escalate" {
			lastErr = fmt.Errorf("review decision must be accept, revise, or escalate, got %q", d.Decision)
			continue
		}
		e.emit(event.New(e.SessionID, taskID, event.SourceReviewer, event.Status, strings.ToUpper(d.Decision)+": "+d.Summary, d))
		return d, nil
	}
	return reviewDecision{}, fmt.Errorf("reviewer could not produce a bounded decision: %w", lastErr)
}

// routePlan asks Sensei whether the region this plan intends to touch is one it
// can certify, and routes authority on the answer.
//
// This is the first preflight in the workflow that can name files: the start
// gate ran before a plan existed. The architect's own decision is not consulted
// here and is not a parameter.
func (e *Engine) routePlan(sc *sensei.Client, start certifiedStart, task string, d architectureDecision) (Routing, error) {
	args := map[string]any{"task": task, "files": d.Files, "mode": "compact"}
	if domain := start.Domain(); domain != "" {
		args["domain"] = domain
	}
	result, err := sc.CallTool("awareness_preflight", args)
	if err != nil {
		return Routing{}, fmt.Errorf("Sensei scoped preflight: %w", err)
	}
	scoped, err := sensei.DecodePreflight(result)
	if err != nil {
		return Routing{}, err
	}
	return routeAuthority(scoped, d.Claims), nil
}

func (e *Engine) awaitHuman(ctx context.Context, sc *sensei.Client, start certifiedStart, taskID string, d architectureDecision, condition string) (string, error) {
	options := d.Options
	if len(options) == 0 {
		options = []authority.Option{
			{ID: "1", Label: "Preserve current human-owned intent and require another design"},
			{ID: "2", Label: "Authorize the architectural change described above"},
			{ID: "3", Label: "Stop this task"},
		}
	}
	if len(options) > 3 {
		options = options[:3]
	}
	// Human authority is deliberately presented as a tiny numbered decision
	// surface. Model-supplied option IDs are not authority; normalize them so
	// the TUI is always an unambiguous 1/2/3 rendezvous.
	for i := range options {
		options[i].ID = fmt.Sprint(i + 1)
	}
	decision := authority.Decision{
		Level:          authority.Human,
		Subject:        d.HumanQuestion,
		Reason:         d.Summary,
		Recommendation: d.Recommendation,
		Options:        options,
	}
	if strings.TrimSpace(decision.Subject) == "" {
		decision.Subject = "Architectural authority reached a human-owned boundary."
	}
	// Every Level-3 interruption states the condition that produced it. An
	// interruption a human cannot trace back to a specific certifiability
	// condition is one they learn to click through.
	if condition = strings.TrimSpace(condition); condition != "" {
		decision.Reason = strings.TrimSpace(condition + "\n\n" + decision.Reason)
	}
	return e.awaitChoice(ctx, sc, taskID, condition, start.Domain(), start.BaseSHA(), decision, options)
}

// awaitChoice presents a numbered decision to the human and blocks until they
// answer. It is the single place a task waits on a person, so every gate --
// architectural authority and plan approval alike -- looks the same in the UI.
func (e *Engine) awaitChoice(ctx context.Context, sc *sensei.Client, taskID, condition, domain, baseSHA string, decision authority.Decision, options []authority.Option) (string, error) {
	ch := make(chan string, 1)
	e.mu.Lock()
	e.pending[taskID] = ch
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.pending, taskID)
		e.mu.Unlock()
	}()
	e.emit(event.New(e.SessionID, taskID, event.SourceArchitect, event.AuthorityRequired, decision.Subject, decision))

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case choice := <-ch:
		for _, option := range options {
			if option.ID == choice {
				// The answer is authoritative for this run the moment it is
				// given. Whether it becomes project knowledge is Sensei's
				// question, asked separately and answered honestly.
				// Only a real certifiability condition is worth teaching the
				// project. An empty condition means this rendezvous was an
				// ordinary product confirmation -- approve this plan, open this
				// pull request -- and filing those as proposed contracts would
				// fill Sensei's review queue with restatements of the UI.
				if strings.TrimSpace(condition) == "" {
					e.emit(event.New(e.SessionID, taskID, event.SourceUser, event.AuthorityResolved, option.Label, map[string]string{"option": option.ID}))
				} else {
					resolution := authority.Resolution{
						TaskID: taskID, SessionID: e.SessionID, Domain: domain, BaseSHA: baseSHA,
						DecidedAt: time.Now().UTC(),
						Question:  decision.Subject, Condition: condition,
						OptionID: option.ID, OptionLabel: option.Label,
					}
					if isStopOption(option.Label) {
						// Stopping is not a governing decision about the
						// architecture, so there is nothing to propose.
						resolution.State = authority.Unsupported
						resolution.Detail = "the human stopped the task rather than resolving the question"
					} else {
						resolution = authority.Persist(senseiProposer{sc}, resolution)
					}
					e.emit(event.New(e.SessionID, taskID, event.SourceUser, event.AuthorityResolved, option.Label, resolution))
					e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status, resolution.Summary(), resolution))
				}

				if isStopOption(option.Label) {
					return "", errors.New("task stopped by human authority")
				}
				return option.ID + ": " + option.Label, nil
			}
		}
		return "", fmt.Errorf("unknown human authority option %q", choice)
	}
}

func isStopOption(label string) bool {
	label = strings.ToLower(strings.TrimSpace(label))
	return strings.Contains(label, "stop") || strings.Contains(label, "cancel") || strings.Contains(label, "abort")
}

func sourceFor(name string) event.Source {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "claude":
		return event.SourceClaude
	case "codex":
		return event.SourceCodex
	default:
		return event.SourceSystem
	}
}

func firstText(r sensei.ToolResult) string {
	if len(r.Content) > 0 && r.Content[0].Text != "" {
		return r.Content[0].Text
	}
	return "Sensei returned structured evidence"
}

func decodeModelJSON(text string, dst any) error {
	s := strings.TrimSpace(text)
	if strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		if len(lines) >= 3 {
			lines = lines[1 : len(lines)-1]
			s = strings.Join(lines, "\n")
		}
	}
	start, end := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return errors.New("model response does not contain a JSON object")
	}
	if err := json.Unmarshal([]byte(s[start:end+1]), dst); err != nil {
		return fmt.Errorf("decode model decision: %w", err)
	}
	return nil
}

func architecturePrompt(repoRoot, domain, architectName, task, conversation, workspaceStatus, preflight string) string {
	if strings.TrimSpace(domain) == "" {
		domain = "(no repository domain is bound in this checkout)"
	}
	return fmt.Sprintf(`You are the architect for this repository, working inside Sensei Code. You are the
single agent the human talks to. Implementation workers (Claude, Codex, and others) never
speak to the human directly: you direct them and you report what they did.

Your identity: %s, acting as architect.
Repository: %s
Repository domain: %s

You have Sensei's own MCP tools available in this session: awareness_briefing,
awareness_preflight, awareness_impact, awareness_query, awareness_resolve, awareness_metadata,
awareness_edit_check, awareness_audit_diff, sensei_workspace_status, task_briefing and
task_status. Use them to check what actually governs a file before you advise on it. Your
shell is sandboxed and cannot reach the network, so do not try to run the sensei CLI or curl
the graph: a shell failure means your shell is sandboxed, never that Sensei is unavailable.
If a tool itself returns an error, report that exact error rather than inferring a cause.

Sensei is the governance authority for architectural truth, invariants, contracts, evidence,
and admission. Do not edit files, do not weaken or reinterpret Sensei's contracts, and do not
claim admission. You may decide ordinary architectural questions autonomously. Escalate only
when the request would change human-owned intent, an invariant, an externally meaningful
contract, a trust boundary, or another explicitly human-owned policy.

Choose ONE decision:

- "reply" when the human is greeting you, asking a question, or wants information,
  discussion, or your opinion rather than a change to the repository. Answer them directly
  and conversationally in "message", as the architect of THIS repository, using the Sensei
  evidence below. If the Sensei evidence shows something worth their attention, say so.
  Never start implementation work for a "reply".
- "proceed" only when the human actually asked for a change to the repository. Give a
  bounded implementation contract in "plan", and break it into "steps": the concrete pieces
  of work, in order, each one a short line an architect can accept or reject on sight. The
  human reads the steps and decides whether the work starts, so write them as commitments,
  not as a description of the request. Also give "consequences", the "files" the work touches,
  and "related_invariants": real Sensei invariant ids that govern those files, which you can
  look up with your Sensei tools. Those three are recorded as an architectural decision when
  the human accepts, so that the reason for this work outlives the session. Do not invent an
  invariant id; leave the list empty if you did not verify one.
- "escalate" when a human-owned authority boundary is genuinely in play.

Return ONLY JSON in this exact shape:
{
  "decision": "reply" | "proceed" | "escalate",
  "message": "your words to the human, only when replying",
  "summary": "concise architectural decision, when proceeding or escalating",
  "plan": "bounded implementation contract, only when proceeding",
  "steps": ["concrete piece of work", "..."],
  "consequences": "what changes for this repository once the plan lands, when proceeding",
  "files": ["path/the/work/touches.go"],
  "related_invariants": ["existing Sensei invariant id this work is governed by"],
  "human_question": "only when escalating",
  "recommendation": "option id only when escalating",
  "options": [{"id":"1","label":"...","description":"..."}],
  "claims": [{"statement":"the factual premise","about":"path or component it concerns","source":"graph|repository|inference"}]
}
When escalating, provide 2 or 3 concrete options. Do not ask about ordinary implementation details.

CLAIMS ARE REQUIRED WHENEVER YOU PROCEED. List every factual premise your plan
depends on, and mark each one honestly:
  graph      - you read it from Sensei (a briefing, an invariant, an impact result)
  repository - you read it in the code or in a file in this repository
  inference  - you concluded it without checking

Mark a premise "inference" whenever you did not actually verify it. That is not a
failure and it is not penalised: it routes the plan to a human for that one
premise, which is exactly what should happen. Claiming "graph" or "repository"
for something you reasoned your way to is the only wrong answer here, because it
converts an unchecked assumption into certified authority. Whether the plan
proceeds is decided from Sensei evidence, not from how confident you sound, so
there is nothing to gain by overstating a source.

You are resumed fresh for every turn, so the dialogue so far is given below. Continue it:
you have already met this person if it is not empty. Introduce yourself and say what this
repository is ONLY when the conversation is empty. Never repeat a greeting, never restate
your role, and never re-describe the repository the human is already working in. Answer what
they actually asked, and keep it short unless they asked for depth.

CONVERSATION SO FAR:
%s

WHAT THE HUMAN JUST SAID:
%s

SENSEI WORKSPACE AUTHORITY:
%s

SENSEI PREFLIGHT:
%s`, architectName, repoRoot, domain, conversationOrNone(conversation), task, workspaceStatus, preflight)
}

func implementationPrompt(tc taskContext, plan, feedback string, cycle int, guidance []string) string {
	extra := ""
	if strings.TrimSpace(feedback) != "" {
		extra = "\n\nREVIEW FEEDBACK TO RECONCILE:\n" + feedback
	}
	if len(guidance) != 0 {
		extra += "\n\nGUIDANCE FROM THE HUMAN ARCHITECT, sent while you were working:\n- " +
			strings.Join(guidance, "\n- ") +
			"\n\nThis is the human speaking directly and it takes precedence over your own" +
			"\njudgement about how to implement the plan. It does not silently enlarge the" +
			"\nplan: if following it requires work the approved plan does not cover, do the" +
			"\npart that is in scope, and say plainly in your output what you did not do and" +
			"\nwhy, so a new plan can be approved for the rest."
	}
	return fmt.Sprintf(`You are a bounded implementation worker operating in an isolated Git worktree.
Implement only the architect's bounded plan. You may inspect, edit, build, and test inside this candidate worktree. Work autonomously: do not ask the user for routine permissions.
Never push, merge, deploy, weaken governance artifacts, rewrite human-owned intent, or claim admission. Sensei Code owns orchestration and Sensei owns governance.

You have Sensei's own MCP tools in this session: awareness_briefing,
awareness_preflight, awareness_impact, awareness_query, awareness_resolve,
awareness_metadata, awareness_edit_check and sensei_workspace_status. Consult
them for any file you are about to change: they hold the invariants that protect
it, the failure modes recorded against it and the fixes forbidden there, none of
which are visible in the code. Every role on this task reads the same graph, so
what you learn there is what the architect and the reviewer are working from too.
If a tool returns an error, report that exact error rather than inferring a cause.

The conversation, architect intent, and Sensei evidence below are given so you
understand WHY this work was asked for and can make the small judgement calls an
implementation always needs. They do NOT widen your scope. Implement the plan and
nothing else. If the conversation implies work the plan does not contain, say so
in your output and leave it undone rather than deciding for the architect.

CONVERSATION WITH THE ARCHITECT:
%s

ARCHITECT INTENT:
%s

SENSEI WORKSPACE AUTHORITY:
%s

SENSEI PREFLIGHT:
%s

TASK:
%s

ARCHITECTURAL PLAN:
%s

CYCLE: %d%s`, conversationOrNone(tc.Conversation), tc.intent(), tc.WorkspaceStatus, tc.Preflight, tc.Task, plan, cycle, extra)
}

func reviewPrompt(tc taskContext, plan, diff, audit, evidence string) string {
	return fmt.Sprintf(`You are the architectural reviewer for a Sensei-governed candidate. Do not edit files.
Decide whether the exact candidate satisfies the architectural plan and the supplied Sensei evidence. Passing tests alone is not architectural proof.
Return ESCALATE only when a genuine architectural-authority question exists; ordinary defects are REVISE.

You have Sensei's own MCP tools in this session: awareness_briefing,
awareness_preflight, awareness_impact, awareness_query, awareness_resolve,
awareness_metadata, awareness_audit_diff and sensei_workspace_status. Check the
candidate against what Sensei actually holds for the files it touches rather than
against your own reading of them, and quote what Sensei said. You, the architect
and the worker all read the same graph, so a disagreement between you is a real
finding rather than a difference of opinion.

The conversation and architect intent below tell you what the human actually
asked for, so you can judge whether the candidate serves it. Context does not
lower the bar: a candidate that matches the conversation but violates the plan or
Sensei's evidence is still a REVISE.

Return ONLY JSON:
{"decision":"accept"|"revise"|"escalate","summary":"...","instructions":"specific repair instructions when revise/escalate"}

CONVERSATION WITH THE ARCHITECT:
%s

ARCHITECT INTENT:
%s

SENSEI WORKSPACE AUTHORITY:
%s

SENSEI PREFLIGHT:
%s

TASK:
%s

ARCHITECTURAL PLAN:
%s

SENSEI DIFF AUDIT:
%s

VALIDATION EVIDENCE:
This is the record of checks the execution broker actually ran against this
exact candidate, with exit statuses. It is produced by executing the commands,
not by the worker reporting that it ran them, so you may rely on it as evidence
rather than as a claim. A check recorded as not-permitted or errored did not run
and proves nothing.
%s

CANDIDATE DIFF:
%s`, conversationOrNone(tc.Conversation), tc.intent(), tc.WorkspaceStatus, tc.Preflight, tc.Task, plan, audit, evidence, diff)
}

func escalationPrompt(task, plan, audit string, review reviewDecision) string {
	return fmt.Sprintf(`A bounded implementation reviewer found a possible architectural boundary. Resolve it using your architectural authority. Escalate to the human only if the decision changes human-owned intent/policy/contract/trust authority. Otherwise issue a revised bounded plan.

TASK:
%s

CURRENT PLAN:
%s

SENSEI AUDIT:
%s

REVIEWER:
%s
%s

Return ONLY the same architecture JSON contract as before.`, task, plan, audit, review.Summary, review.Instructions)
}

func humanResolutionPrompt(original string, d architectureDecision, choice string) string {
	return fmt.Sprintf(`%s

HUMAN AUTHORITY RESOLUTION:
The human selected %s.
Apply that decision exactly. You now have architectural authority to produce a bounded implementation plan within that human choice.
Return ONLY architecture JSON with decision="proceed" unless the human choice itself exposes a different unresolved human-owned boundary.

PREVIOUS ESCALATION:
%s`, original, choice, d.Summary)
}

// certifiedResolutionPrompt answers an architect that asked to escalate a
// question Sensei can in fact certify.
//
// The reply is deliberately not "you were wrong to ask". The architect gets the
// certification as evidence and is asked to decide on it, because the useful
// behaviour to reinforce is asking when unsure, not staying quiet — what must
// not happen is the asking alone interrupting a human.
func certifiedResolutionPrompt(original string, d architectureDecision) string {
	return fmt.Sprintf(`%s

AUTHORITY ROUTING RESULT:
You asked to escalate this to a human. Sensei certifies the region your plan
touches: the graph is authoritative and current, it covers the planned files,
and it reports no blind spots or approval gate for this change class.

That means this question has architectural authority and does not require a
human. Decide it yourself on the evidence and return ONLY architecture JSON with
decision="proceed" and a bounded plan.

If you still believe a human must decide, the plan must name the specific
premise you cannot verify as a claim with source="inference"; a general feeling
of risk is not a human-owned boundary.

YOUR PREVIOUS ESCALATION:
%s`, original, d.Summary)
}

// conversationOrNone keeps the architect prompt unambiguous: an empty section
// could read as truncation, so a first turn says so explicitly.
func conversationOrNone(conversation string) string {
	if strings.TrimSpace(conversation) == "" {
		return "(nothing yet - this is the first thing they have said to you)"
	}
	return conversation
}

// implement drives the candidate from an approved plan to an accepted change.
// It is separate from run so a resumed task can enter here: the architect has
// already decided and the human has already approved, and asking either of them
// again would be inventing a decision that was made before the restart.
func (e *Engine) implement(ctx context.Context, sc *sensei.Client, start certifiedStart, taskID string, tc *taskContext, plan, carried string, fail func(error)) {
	task := tc.Task
	// One candidate for the whole task. A worker that runs out of review cycles
	// hands the work on rather than taking it with it, so the next worker
	// continues from the same checkout and keeps every fix the reviewer has
	// already extracted.
	// The base is established before the worktree exists and is never
	// recomputed afterwards, so a resumed task continues from the state its
	// plan was approved against rather than from wherever HEAD has moved to.
	identity, idErr := candidate.Establish(
		e.Repo.Root, taskID, start.Domain(),
		e.Repo.WorktreePath(taskID), e.Repo.WorktreeBranch(taskID),
		repoHead{ctx: ctx, repo: e.Repo}, time.Now(),
	)
	if idErr != nil {
		fail(idErr)
		return
	}
	if bound, err := identity.BindGraph(e.Repo.Root, start.GraphBuildCommit(), start.SourceRepoCommit()); err == nil {
		identity = bound
	}
	tc.Identity = identity

	workspace, createErr := e.Repo.CreateWorktreeAt(ctx, taskID, identity.BaseSHA)
	if createErr != nil {
		fail(fmt.Errorf("create the candidate worktree: %w", createErr))
		return
	}
	e.emit(event.New(e.SessionID, taskID, event.SourceGit, event.Status, identity.Summary(), identity))

	// The semantic position of the task, kept so a change of worker does not
	// restart the thinking. It is rebuilt from what is known now rather than
	// trusted from disk, and it carries no authority: a lost state file must
	// mean re-certifying, never proceeding on a remembered yes.
	state := e.taskState(taskID, tc, identity, start)

	// carried arrives non-empty on a resume, so the first worker starts from the
	// findings the interrupted run had already earned.
	var failures []string
	for _, worker := range e.Config.Implementors {
		state.RecordWorker(worker.Name)
		state.Phase = taskstate.Implementing
		_ = state.Save(e.Repo.Root)
		if carried != "" {
			e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status,
				config.DisplayName(worker.Name)+" is continuing the existing candidate, not starting over", nil))
		}
		accepted, finalPlan, review, audit, err := e.runCandidate(ctx, sc, start, taskID, tc, plan, worker, workspace, carried)
		if err != nil {
			failures = append(failures, worker.Name+": "+err.Error())
			// The candidate stays: it holds real work, and the reviewer's
			// unresolved findings travel with it to whoever picks it up next.
			state.Phase = taskstate.Revising
			state.Evidence = tc.EvidenceSnapshot
			state.OpenFindings(openFindings(review, audit, err))
			_ = state.Save(e.Repo.Root)
			carried = state.Handover(config.DisplayName(worker.Name), start.GraphBuildCommit())
			e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status, worker.Name+" did not converge; handing the candidate to the next bounded worker", map[string]string{"error": err.Error()}))
			continue
		}
		plan = finalPlan
		if accepted {
			state.Phase = taskstate.Accepted
			state.Evidence = tc.EvidenceSnapshot
			state.OpenFindings(nil)
			_ = state.Save(e.Repo.Root)
			e.reportUndeliveredNotes(taskID)
			e.offerPullRequest(ctx, taskID, tc, workspace, worker.Name)
			e.reportOutcome(ctx, "success", task, "candidate ready for governed admission")
			e.emit(event.New(e.SessionID, taskID, event.SourceGit, event.Status,
				"candidate kept for inspection at "+workspace, nil))
			e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.WorkflowCompleted, "candidate ready for governed admission", map[string]any{
				"workspace":   workspace,
				"implementor": worker.Name,
				"plan":        plan,
				"review":      review,
				"audit":       audit,
			}))
			return
		}
	}

	// Nothing converged, but the candidate is not thrown away. It carries the
	// accumulated work and the reviewer's outstanding findings, which is what a
	// person needs to finish it or to judge whether it was worth starting.
	e.emit(event.New(e.SessionID, taskID, event.SourceGit, event.Status,
		"candidate kept so the work is not lost: "+workspace, nil))
	e.reportUndeliveredNotes(taskID)
	fail(fmt.Errorf("no bounded implementor produced an acceptable candidate: %s", strings.Join(failures, " | ")))
}

// Resume continues a task that was interrupted after its plan was approved. The
// candidate worktree still holds the work, so this re-enters the implementation
// stage with the reviewer's last findings rather than re-deciding the plan.
func (e *Engine) Resume(ctx context.Context, task session.Interrupted) string {
	go func() {
		// A resumed task keeps the mode it was running in. Resumption is not a
		// new entry point a person chose, so its provenance says so.
		e.announceMode(task.TaskID, governedMode(ResumedGoverned))
		fail := func(err error) {
			e.emit(event.New(e.SessionID, task.TaskID, event.SourceSystem, event.WorkflowFailed, err.Error(), nil))
			e.reportOutcome(ctx, "failure", task.Task, err.Error())
		}
		sc, err := sensei.Start(ctx, e.Repo.Root, e.Config.Sensei.Command, e.Config.Sensei.Args)
		if err != nil {
			fail(fmt.Errorf("start Sensei: %w", err))
			return
		}
		defer sc.Close()

		// Evidence is re-read rather than restored from the log. The graph may
		// have moved while the process was gone, and a resumed task must be
		// governed by what Sensei says now.
		workspaceStatus, err := sc.CallTool("sensei_workspace_status", map[string]any{"repo": e.Repo.Root})
		if err != nil {
			fail(fmt.Errorf("Sensei workspace status: %w", err))
			return
		}
		e.emit(event.New(e.SessionID, task.TaskID, event.SourceSensei, event.SenseiResult, firstText(workspaceStatus), workspaceStatus.Structured))

		// Resume re-runs the full start gate, not just the workspace read. A
		// task interrupted an hour ago was certified against the graph as it
		// stood then; the graph may have been rebuilt, gone stale, or lost its
		// domain binding since. Resuming on the strength of the earlier
		// certification would carry a receipt across a gap in which the thing
		// it certified changed.
		preflightArgs := map[string]any{"task": task.Task, "files": []string{}, "mode": "compact"}
		if domain := sensei.RepositoryDomain(workspaceStatus); domain != "" {
			preflightArgs["domain"] = domain
		}
		preflight, err := sc.CallTool("awareness_preflight", preflightArgs)
		if err != nil {
			fail(fmt.Errorf("Sensei preflight: %w", err))
			return
		}
		e.emit(event.New(e.SessionID, task.TaskID, event.SourceSensei, event.SenseiResult, firstText(preflight), preflight.Structured))

		start, err := certifyStart(workspaceStatus, preflight)
		if err != nil {
			e.emit(event.New(e.SessionID, task.TaskID, event.SourceSensei, event.Status, err.Error(), preflight.Structured))
			e.reportOutcome(ctx, "blocked", task.Task, err.Error())
			fail(err)
			return
		}

		tc := taskContext{
			Task:            task.Task,
			Conversation:    e.conversationSoFar(task.Task, 40),
			WorkspaceStatus: firstText(workspaceStatus),
			Preflight:       firstText(preflight),
			Rationale:       task.Plan,
			Domain:          start.Domain(),
		}
		carried := ""
		if r := strings.TrimSpace(task.Review); r != "" {
			carried = "This candidate was interrupted before it converged. Its changes are already present.\n\nThe last review said:\n" + r
		}
		e.emit(event.New(e.SessionID, task.TaskID, event.SourceSystem, event.Status,
			"resuming the interrupted candidate rather than starting over", nil))
		e.implement(ctx, sc, start, task.TaskID, &tc, task.Plan, carried, fail)
	}()
	return task.TaskID
}

// repoHead adapts the context-taking git surface to the narrow interface the
// candidate package needs, so identity policy stays testable without a repo.
type repoHead struct {
	ctx  context.Context
	repo gitx.Repo
}

func (r repoHead) Head() (string, error)  { return r.repo.Head(r.ctx) }
func (r repoHead) IsClean() (bool, error) { return r.repo.IsClean(r.ctx) }

// senseiProposer adapts the MCP client to the narrow write surface the
// authority package needs, so resolutions can be submitted without that package
// depending on the transport.
type senseiProposer struct{ sc *sensei.Client }

func (p senseiProposer) CallTool(name string, args map[string]any) (authority.ToolResult, error) {
	if p.sc == nil {
		return authority.ToolResult{}, errors.New("Sensei is unavailable, so the resolution cannot be proposed")
	}
	result, err := p.sc.CallTool(name, args)
	if err != nil {
		return authority.ToolResult{}, err
	}
	return authority.ToolResult{Structured: result.Structured, Text: firstText(result), IsError: result.IsError}, nil
}

// taskState assembles the semantic position of a task from what is currently
// known. It is deliberately a projection of live facts rather than a record
// loaded from disk: the file exists so a later process can read the position,
// not so this one can skip establishing it.
func (e *Engine) taskState(taskID string, tc *taskContext, id candidate.Identity, start certifiedStart) taskstate.State {
	required := make([]string, 0, len(start.RequiredActions()))
	required = append(required, start.RequiredActions()...)
	return taskstate.State{
		TaskID: taskID, SessionID: e.SessionID, Task: tc.Task, Domain: id.Domain,
		BaseSHA: id.BaseSHA, Worktree: id.Worktree, Branch: id.Branch,
		Contract: taskstate.Contract{
			Rationale:    tc.Rationale,
			Steps:        tc.Steps,
			Consequences: tc.Consequences,
			Invariants:   tc.Invariants,
		},
		Authority:        e.authorityDecisions(taskID),
		Evidence:         taskstate.Evidence{RequiredTests: required},
		Phase:            taskstate.Planning,
		GraphBuildCommit: start.GraphBuildCommit(),
		ObservedAt:       time.Now().UTC(),
	}
}

// authorityDecisions reads the human decisions already made for this task out
// of the session record, so a worker never reopens a settled question.
//
// It reads the event log rather than a decisions file, because the event log is
// the record that already exists and a second one would need reconciling.
func (e *Engine) authorityDecisions(taskID string) []taskstate.AuthorityDecision {
	if e.Store == nil {
		return nil
	}
	events, err := e.Store.Load()
	if err != nil {
		return nil
	}
	var out []taskstate.AuthorityDecision
	for _, ev := range events {
		if ev.TaskID != taskID || ev.Kind != event.AuthorityResolved {
			continue
		}
		var res authority.Resolution
		if json.Unmarshal(ev.Payload, &res) != nil || strings.TrimSpace(res.Question) == "" {
			continue
		}
		out = append(out, taskstate.AuthorityDecision{
			Question:  res.Question,
			Condition: res.Condition,
			Chosen:    res.OptionLabel,
			Durable:   res.Durable(),
			DecidedAt: res.DecidedAt,
		})
	}
	return out
}

// openFindings turns what the last cycle produced into the list the next worker
// has to clear. An empty list is left empty rather than padded: inventing a
// finding to look thorough would send the next worker after nothing.
func openFindings(review, audit string, cause error) []taskstate.Finding {
	var out []taskstate.Finding
	if r := strings.TrimSpace(review); r != "" {
		out = append(out, taskstate.Finding{Source: "reviewer", Detail: r})
	}
	if a := strings.TrimSpace(audit); a != "" {
		out = append(out, taskstate.Finding{Source: "sensei audit", Detail: a})
	}
	if cause != nil {
		out = append(out, taskstate.Finding{Source: "previous worker stopped", Detail: cause.Error()})
	}
	return out
}

// answeredConditions reports the certifiability conditions this task has
// already put to the human, and whether each answer authorized the work.
//
// It exists because authorizing does not change the graph. The router reads
// Sensei, Sensei still reports the region uncovered, and without this the same
// condition escalates again on the very next plan -- which is what happened:
// one acceptance run asked the human the identical question thirteen times and
// never reached a candidate. The human's answer has to be remembered by the
// thing that would otherwise ask again.
//
// This is deliberately run-scoped and read from the session record. It is not a
// cache of canon and it does not make the answer project knowledge: that path
// is authority.Persist, and it stays separate. What is remembered here binds
// this task only, which is exactly the status a resolution has before Sensei
// promotes it.
func (e *Engine) answeredConditions(taskID string) map[string]bool {
	out := map[string]bool{}
	if e.Store == nil {
		return out
	}
	events, err := e.Store.Load()
	if err != nil {
		return out
	}
	for _, ev := range events {
		if ev.TaskID != taskID || ev.Kind != event.AuthorityResolved {
			continue
		}
		var res authority.Resolution
		if json.Unmarshal(ev.Payload, &res) != nil {
			continue
		}
		if c := strings.TrimSpace(res.Condition); c != "" {
			out[c] = authority.Authorizes(res.OptionLabel)
		}
	}
	return out
}

// applyAnsweredCondition decides what to do with a routing whose condition the
// human has already settled for this task.
//
// authorized reports whether the run may proceed without asking again. asked
// reports whether the question has been put at all, so a caller can tell "the
// human said yes" from "the human has not been asked".
func (e *Engine) applyAnsweredCondition(taskID, condition string) (authorized, asked bool) {
	answer, ok := e.answeredConditions(taskID)[strings.TrimSpace(condition)]
	return answer, ok
}

// oneLine flattens a multi-line review into something readable inside an error.
func oneLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " · "))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

// validate runs the required checks and returns evidence bound to the candidate
// content the reviewer will actually see.
//
// The returned diff may differ from the one passed in: a formatter rewrites the
// candidate, so the diff is re-read after formatting and the certifying checks
// are bound to that. Returning the new diff rather than mutating in place makes
// it impossible for a caller to keep using the pre-format bytes by accident.
func (e *Engine) validate(ctx context.Context, taskID, base string, envelope broker.Envelope, repo gitx.Repo, diff string) (validation.Bundle, string, error) {
	permits := func(kind validation.CheckKind) (bool, string) {
		var capability broker.Capability
		switch kind {
		case validation.Format:
			capability = broker.RunFormatters
		case validation.Build:
			capability = broker.RunBuilds
		case validation.Vet:
			capability = broker.RunBuilds
		case validation.Test:
			capability = broker.RunTests
		default:
			return false, "unknown check kind " + string(kind)
		}
		if err := envelope.Require(capability); err != nil {
			return false, err.Error()
		}
		return true, ""
	}
	runner := validation.Runner{Workspace: repo.Root, Permits: permits}

	// Formatting first, and its evidence is deliberately discarded from the
	// certifying bundle: it describes the candidate before the rewrite.
	if formats := checksOf(validation.Format, e.Config.Validation.Format); len(formats) != 0 {
		runner.Run(ctx, taskID, validation.Digest(diff), formats)
		reread, err := repo.CandidateDiff(ctx, base)
		if err != nil {
			return validation.Bundle{}, diff, err
		}
		diff = reread
	}

	var checks []validation.Check
	checks = append(checks, checksOf(validation.Vet, e.Config.Validation.Vet)...)
	checks = append(checks, checksOf(validation.Build, e.Config.Validation.Build)...)
	checks = append(checks, checksOf(validation.Test, e.Config.Validation.Test)...)
	return runner.Run(ctx, taskID, validation.Digest(diff), checks), diff, nil
}

func checksOf(kind validation.CheckKind, commands []config.Command) []validation.Check {
	out := make([]validation.Check, 0, len(commands))
	for _, c := range commands {
		if strings.TrimSpace(c.Command) == "" {
			continue
		}
		out = append(out, validation.Check{Kind: kind, Command: c.Command, Args: c.Args, Mutates: kind == validation.Format})
	}
	return out
}

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
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/decision"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/sensei"
	"github.com/globulario/sensei-code/internal/session"
)

type Engine struct {
	Repo      gitx.Repo
	Config    config.Config
	Bus       *event.Bus
	Store     *session.Store
	SessionID string

	mu      sync.Mutex
	pending map[string]chan string
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

func (e *Engine) Submit(ctx context.Context, task string) string {
	taskID := fmt.Sprintf("task-%d", time.Now().UTC().UnixNano())
	go e.run(ctx, taskID, strings.TrimSpace(task))
	return taskID
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
	fail := func(err error) {
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.WorkflowFailed, err.Error(), nil))
	}
	if task == "" {
		fail(errors.New("task is empty"))
		return
	}
	if !e.Config.Permissions.ReadRepository {
		fail(errors.New("repository read capability is not granted"))
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

	conversation := e.conversationSoFar(task, 40)
	decision, err := e.resolveArchitecture(ctx, taskID, architecturePrompt(e.Repo.Root, sensei.RepositoryDomain(workspaceStatus), config.DisplayName(e.Config.Architect.Name), task, conversation, firstText(workspaceStatus), firstText(preflight)))
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
		e.emit(event.New(e.SessionID, taskID, event.SourceArchitect, event.ArchitectSpoke,
			"Holding off. Nothing has been implemented -- tell me what to change about the plan.", nil))
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.WorkflowCompleted, "", nil))
		return
	}
	e.recordDecision(ctx, taskID, task, decision, sensei.RepositoryDomain(workspaceStatus))
	plan := decision.Plan
	tc := taskContext{
		Task:            task,
		Conversation:    conversation,
		WorkspaceStatus: firstText(workspaceStatus),
		Preflight:       firstText(preflight),
		Rationale:       decision.Summary,
		Steps:           decision.Steps,
	}

	if !e.Config.Permissions.CreateWorktrees || !e.Config.Permissions.WriteCandidates {
		fail(errors.New("candidate worktree capability is not granted"))
		return
	}
	if len(e.Config.Implementors) == 0 {
		fail(errors.New("no implementor is configured"))
		return
	}

	var failures []string
	for _, worker := range e.Config.Implementors {
		workspace, err := e.Repo.CreateWorktree(ctx, taskID, worker.Name)
		if err != nil {
			failures = append(failures, worker.Name+": "+err.Error())
			continue
		}
		e.emit(event.New(e.SessionID, taskID, event.SourceGit, event.Status, "isolated candidate worktree: "+workspace, map[string]string{"worker": worker.Name, "workspace": workspace}))

		accepted, finalPlan, review, audit, err := e.runCandidate(ctx, sc, taskID, tc, plan, worker, workspace)
		if err != nil {
			failures = append(failures, worker.Name+": "+err.Error())
			e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status, worker.Name+" candidate did not converge; trying next bounded worker", map[string]string{"error": err.Error()}))
			continue
		}
		plan = finalPlan
		if accepted {
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

	fail(fmt.Errorf("no bounded implementor produced an acceptable candidate: %s", strings.Join(failures, " | ")))
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
	choice, err := e.awaitChoice(ctx, taskID, decision, options)
	if err != nil {
		return false, err
	}
	return strings.HasPrefix(choice, "1:"), nil
}

// recordDecision writes the accepted plan into Sensei as an architectural
// decision, so the reason this work was authorized outlives the session and any
// agent can read it later. A decision Sensei would refuse is reported, never
// padded with invented links to make it pass.
func (e *Engine) recordDecision(ctx context.Context, taskID, task string, d architectureDecision, domain string) {
	record := decision.Record{
		Title:        strings.TrimSpace(d.Summary),
		Rationale:    task,
		Context:      "Accepted by the human in an interactive Sensei Code session.",
		Consequences: d.Consequences,
		SourceFiles:  d.Files,
		Invariants:   d.Invariants,
		Repo:         domain,
		Domain:       domain,
		RepoRoot:     e.Repo.Root,
	}
	if strings.TrimSpace(record.Title) == "" {
		record.Title = task
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

func (e *Engine) runCandidate(ctx context.Context, sc *sensei.Client, taskID string, tc taskContext, initialPlan string, worker config.Agent, workspace string) (bool, string, string, string, error) {
	task := tc.Task
	plan := initialPlan
	feedback := ""
	var lastReview, lastAudit string

	for cycle := 1; cycle <= e.Config.Workflow.ReviewCycles; cycle++ {
		prompt := implementationPrompt(tc, plan, feedback, cycle)
		impl := agent.CLI{Name: worker.Name, Label: config.DisplayName(worker.Name), Command: worker.Command, Args: worker.Args, Source: sourceFor(worker.Name), SessionID: e.SessionID}
		if _, err := impl.Run(ctx, agent.Request{Role: agent.Implementor, TaskID: taskID, Workspace: workspace, Prompt: prompt}, e.emit); err != nil {
			return false, plan, lastReview, lastAudit, fmt.Errorf("implementor cycle %d: %w", cycle, err)
		}

		candidate := gitx.Repo{Root: workspace}
		diff, err := candidate.Diff(ctx)
		if err != nil {
			return false, plan, lastReview, lastAudit, err
		}
		if strings.TrimSpace(diff) == "" {
			return false, plan, lastReview, lastAudit, errors.New("implementor produced no candidate diff")
		}
		e.emit(event.New(e.SessionID, taskID, event.SourceGit, event.CandidateChanged, fmt.Sprintf("candidate diff %d bytes · cycle %d", len(diff), cycle), nil))

		audit, err := sc.CallTool("awareness_audit_diff", map[string]any{"diff": diff, "task": task})
		if err != nil {
			return false, plan, lastReview, lastAudit, fmt.Errorf("Sensei diff audit: %w", err)
		}
		lastAudit = firstText(audit)
		e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.CandidateAudited, lastAudit, audit.Structured))

		review, err := e.resolveReview(ctx, taskID, reviewPrompt(tc, plan, diff, lastAudit))
		if err != nil {
			return false, plan, lastReview, lastAudit, err
		}
		lastReview = review.Summary
		switch review.Decision {
		case "accept":
			return true, plan, lastReview, lastAudit, nil
		case "revise":
			feedback = review.Instructions
			if strings.TrimSpace(feedback) == "" {
				feedback = review.Summary
			}
			e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status, "review requested bounded revision; continuing autonomously", map[string]int{"cycle": cycle}))
		case "escalate":
			revised, err := e.resolveArchitecture(ctx, taskID, escalationPrompt(task, plan, lastAudit, review))
			if err != nil {
				return false, plan, lastReview, lastAudit, err
			}
			if strings.TrimSpace(revised.Plan) == "" {
				return false, plan, lastReview, lastAudit, errors.New("architect did not return a revised bounded plan")
			}
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
}

type reviewDecision struct {
	Decision     string `json:"decision"`
	Summary      string `json:"summary"`
	Instructions string `json:"instructions,omitempty"`
}

func (e *Engine) resolveArchitecture(ctx context.Context, taskID, prompt string) (architectureDecision, error) {
	architect := agent.CLI{Name: e.Config.Architect.Name, Label: config.DisplayName(e.Config.Architect.Name), Command: e.Config.Architect.Command, Args: e.Config.Architect.Args, Source: event.SourceArchitect, SessionID: e.SessionID}
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
			e.emit(event.New(e.SessionID, taskID, event.SourceArchitect, event.Status, d.Summary, d))
			return d, nil
		case "escalate":
			choice, err := e.awaitHuman(ctx, taskID, d)
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
	reviewer := agent.CLI{Name: e.Config.Reviewer.Name, Label: config.DisplayName(e.Config.Reviewer.Name), Command: e.Config.Reviewer.Command, Args: e.Config.Reviewer.Args, Source: event.SourceReviewer, SessionID: e.SessionID}
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

func (e *Engine) awaitHuman(ctx context.Context, taskID string, d architectureDecision) (string, error) {
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
	return e.awaitChoice(ctx, taskID, decision, options)
}

// awaitChoice presents a numbered decision to the human and blocks until they
// answer. It is the single place a task waits on a person, so every gate --
// architectural authority and plan approval alike -- looks the same in the UI.
func (e *Engine) awaitChoice(ctx context.Context, taskID string, decision authority.Decision, options []authority.Option) (string, error) {
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
				e.emit(event.New(e.SessionID, taskID, event.SourceUser, event.AuthorityResolved, option.Label, map[string]string{"option": option.ID}))
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
  "options": [{"id":"1","label":"...","description":"..."}]
}
When escalating, provide 2 or 3 concrete options. Do not ask about ordinary implementation details.

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

func implementationPrompt(tc taskContext, plan, feedback string, cycle int) string {
	extra := ""
	if strings.TrimSpace(feedback) != "" {
		extra = "\n\nREVIEW FEEDBACK TO RECONCILE:\n" + feedback
	}
	return fmt.Sprintf(`You are a bounded implementation worker operating in an isolated Git worktree.
Implement only the architect's bounded plan. You may inspect, edit, build, and test inside this candidate worktree. Work autonomously: do not ask the user for routine permissions.
Never push, merge, deploy, weaken governance artifacts, rewrite human-owned intent, or claim admission. Sensei Code owns orchestration and Sensei owns governance.

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

func reviewPrompt(tc taskContext, plan, diff, audit string) string {
	return fmt.Sprintf(`You are the architectural reviewer for a Sensei-governed candidate. Do not edit files.
Decide whether the exact candidate satisfies the architectural plan and the supplied Sensei evidence. Passing tests alone is not architectural proof.
Return ESCALATE only when a genuine architectural-authority question exists; ordinary defects are REVISE.

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

CANDIDATE DIFF:
%s`, conversationOrNone(tc.Conversation), tc.intent(), tc.WorkspaceStatus, tc.Preflight, tc.Task, plan, audit, diff)
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

// conversationOrNone keeps the architect prompt unambiguous: an empty section
// could read as truncation, so a first turn says so explicitly.
func conversationOrNone(conversation string) string {
	if strings.TrimSpace(conversation) == "" {
		return "(nothing yet - this is the first thing they have said to you)"
	}
	return conversation
}

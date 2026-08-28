package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	"github.com/globulario/sensei-code/internal/derived"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/finding"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/provider"
	"github.com/globulario/sensei-code/internal/publish"
	"github.com/globulario/sensei-code/internal/report"
	"github.com/globulario/sensei-code/internal/roles"
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
	// stops holds one cancel per running governed task, so the human can end a
	// run they no longer want. Assisted turns are not registered: a
	// conversation has nothing to withdraw.
	stops map[string]context.CancelFunc
	// observing records which tasks entered through the observation lane.
	//
	// Kept on the engine rather than passed through the plan, for the same
	// reason ActionStage is not a provider field: the lane is a property of how
	// the task was submitted, and anything a plan could set is a claim.
	observing map[string]bool
	// objectives records what each task was asked to do and what established
	// that anyone asked. Kept beside observing and for the same reason: it is a
	// property of how the task was submitted, and a plan that could set it
	// would be making a claim rather than carrying a fact.
	objectives map[string]Objective
	// supplied holds the plan handed in for tasks that entered through
	// SubmitGovernedWithPlan. Absence means the architect authored the bound.
	supplied map[string]SuppliedPlan
	// prospective holds, per task, the prospective grants the router read, so
	// the post-creation inspection checks the created file against the same
	// facts about the covering surface that authorized it.
	prospective map[string][]prospectiveGrant
	// graphs records, per task, the awareness graph the start gate verified,
	// as the MCP binding every agent in that task is launched with.
	//
	// Kept on the engine and set only by the gate, so the binding an agent
	// receives is the one the engine admitted and not one a plan, a prompt or
	// a user's global config could substitute. The first foreign-repository run
	// had the engine on one graph and the architect on another, silently.
	graphs map[string]*agent.GraphBinding
	// findings holds what each observation established, so a caller may open
	// repair work from it. Holding evidence is not holding authority: a repair
	// opened from one enters the ordinary governed path with nothing carried
	// over except the objective text.
	findings map[string][]finding.Finding
	// routings holds what the authority router read when it decided each task,
	// kept because two later decisions ask different questions of the same
	// evidence: how adversarially the candidate must be judged, and whether the
	// change is routine at all.
	//
	// It is recorded at routing time rather than re-derived, because a second
	// preflight would describe a possibly-moved graph while appearing to
	// describe the moment the plan was authorised.
	routings map[string]routingRecord
	// closures counts gap-closure rounds already spent on one condition within
	// one task, so a gap that does not actually close cannot loop forever.
	//
	// Keyed by task and condition rather than by task alone: a run may
	// legitimately meet several different gaps, and spending one budget across
	// all of them would send the second gap straight to a human because the
	// first one used the attempts.
	closures map[string]int
	// premises are the engine-issued receipts for the knowledge gaps each task
	// has met, the identity the closure budget is spent against. See
	// premise.go.
	premises map[string][]*premiseReceipt
	// testEdits are the existing-test edit grants the router read per task
	// (M2.2): operational authority, kept apart from coverage by type.
	testEdits map[string][]testEditGrant
}

// closureBudget is how many rounds one condition gets to close its own gap.
//
// One. A second round on the identical condition means the first produced
// nothing the router could see, and repeating it burns provider calls to
// re-derive the same non-answer. When it is spent the gap is reported as
// unclosed and the question becomes a human's — honestly, and naming what was
// attempted.
const closureBudget = 1

// bindGraph records the graph the start gate verified for a task.
//
// Built from the engine's OWN MCP command and the workspace status it just
// decoded, so by construction it names the graph the engine reached. Every
// agent request for the task carries it, and each provider is launched so it
// can reach this graph and no other.
func (e *Engine) bindGraph(taskID string, ws sensei.WorkspaceStatus) {
	b := &agent.GraphBinding{
		Command: e.Config.Sensei.Command,
		Args:    append([]string(nil), e.Config.Sensei.Args...),
		Domain:  ws.Binding.RepositoryDomain,
	}
	if ws.GraphAuthority != nil {
		b.Digest = ws.GraphAuthority.GraphBuildCommit
	}
	e.mu.Lock()
	if e.graphs == nil {
		e.graphs = map[string]*agent.GraphBinding{}
	}
	e.graphs[taskID] = b
	e.mu.Unlock()
	e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status,
		fmt.Sprintf("graph binding for every agent in this task: domain %s, build %s, via %s %s",
			b.Domain, short12(b.Digest), b.Command, strings.Join(b.Args, " ")), nil))
}

// graphFor is the binding an agent request carries. Nil only before the gate
// has run, which for a governed task is never.
func (e *Engine) graphFor(taskID string) *agent.GraphBinding {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.graphs[taskID]
}

func short12(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	if s == "" {
		return "(none)"
	}
	return s
}

// spendClosure records an attempt at one gap and reports whether the budget
// allowed it.
//
// gap is the premise receipt's ID (premise.go): the engine's own identity for
// the question, carried through the closure loop. Keying on the condition
// text let a paraphrase buy a fresh round (sensei-code#97).
func (e *Engine) spendClosure(taskID, gap string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closures == nil {
		e.closures = map[string]int{}
	}
	key := taskID + "\x00" + gap
	if e.closures[key] >= closureBudget {
		return false
	}
	e.closures[key]++
	return true
}

// routingRecord is the evidence one plan was routed on.
type routingRecord struct {
	Policy roles.Policy
	Scoped sensei.PreflightDecision
	Claims []Claim
	// Planned is what the approved plan named, so a later widening is
	// detectable against the decision rather than against the diff.
	Planned []string
}

// setRouting records what the router read when it decided this task.
func (e *Engine) setRouting(taskID string, p roles.Policy, scoped sensei.PreflightDecision, claims []Claim, planned []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.routings == nil {
		e.routings = map[string]routingRecord{}
	}
	e.routings[taskID] = routingRecord{Policy: p, Scoped: scoped, Claims: claims, Planned: planned}
}

// routingFor returns what was recorded, and whether anything was.
func (e *Engine) routingFor(taskID string) (routingRecord, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.routings[taskID]
	return r, ok
}

// policyFor returns the recorded reading, or the fail-closed one.
//
// An absent policy is the case that matters. It means no routing recorded a
// risk verdict for this task, which is not the same as a task that was judged
// low risk — and reading it as low risk is the same mistake the old change-risk
// parser made when it returned "" and the caller took it for permission.
func (e *Engine) policyFor(taskID string) roles.Policy {
	if r, ok := e.routingFor(taskID); ok {
		return r.Policy
	}
	return roles.PolicyFor("", "")
}

// reviewCapabilities is the bounded set of providers that may take an
// adversarial role, built from the read-only review roster rather than from
// every configured agent. An implementor's argv carries write capability, and a
// reviewer able to fix what it is attacking can report it clean.
func (e *Engine) reviewCapabilities() roles.Capabilities {
	var caps roles.Capabilities
	for _, a := range e.Config.ReviewRoster() {
		caps = append(caps, provider.Capability(a.Name))
	}
	return caps
}

// reviewAgent resolves an assigned provider back to the argv it runs under.
func (e *Engine) reviewAgent(name string) (config.Agent, bool) {
	for _, a := range e.Config.ReviewRoster() {
		if strings.EqualFold(a.Name, name) {
			return a, true
		}
	}
	return config.Agent{}, false
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

// deferToken is what DeferAuthority puts on the pending channel. It is not an
// option ID and cannot collide with one: options are normalised to "1", "2",
// "3" before the question is ever shown.
const deferToken = "\x00defer"

// errAuthorityDeferred reports a Level-3 question the human left standing.
//
// It is an error only in the sense that the run cannot continue — nothing
// failed, nothing was decided, and nothing is cleaned up.
var errAuthorityDeferred = errors.New("the human deferred a Level-3 authority decision")

// DeferAuthority leaves a Level-3 question unanswered without answering it.
//
// The human may always stop computation. What they cannot do with a keystroke
// is satisfy an authority boundary: the router established a condition, and
// only choosing one of its options can move the workflow past it. Deferring
// says "not now" and preserves the question exactly as asked.
//
// It reports whether the task was actually waiting on a decision, so a caller
// never tells a person it paused something that was not asking them anything.
func (e *Engine) DeferAuthority(taskID string) bool {
	e.mu.Lock()
	ch, ok := e.pending[taskID]
	e.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- deferToken:
		return true
	default:
		return false
	}
}

// DeferredAuthority is the settled history of a deferred Level-3 question:
// everything needed to ask exactly it again, and nothing that would let it be
// re-derived.
//
// Re-running the router on resume would look equivalent and is not. The graph,
// the model and the environment may all have moved, so a replay can produce a
// different question — and answering a different question would quietly satisfy
// a boundary nobody was asked about.
type DeferredAuthority struct {
	Condition string             `json:"condition"`
	Domain    string             `json:"domain"`
	BaseSHA   string             `json:"base_sha"`
	Decision  authority.Decision `json:"decision"`
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
	return e.windowFrom(events, current, limit)
}

// windowFrom builds the bounded conversation window from a recorded session. It
// is separated from reading the store so the bound itself can be tested, which
// is the part with a rule attached.
func (e *Engine) windowFrom(events []event.Event, current string, limit int) string {
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
		// Bounded, and it says so. A conversation that silently starts at turn
		// 41 reads to the architect as a conversation that began there, and it
		// will answer confidently about a beginning it never saw.
		dropped := len(turns) - limit
		turns = append([]string{fmt.Sprintf("(%d earlier turns are not shown; this conversation did not start here)", dropped)},
			turns[dropped:]...)
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
	// Mode is the plan's declaration of whether it changes the repository.
	// See architectureDecision.Mode.
	Mode         string
	Consequences string
	// Files are the paths the plan names. The candidate boundary treats them
	// as the change's intended outputs; a binary anywhere else is an artifact.
	Files      []string
	Invariants []string
	// Prospective is the plan's declared new surfaces. See
	// architectureDecision.ProspectiveSurfaces.
	Prospective []ProspectiveSurface
	// PlanSource and PlanDigest say who authored the bound. See PlanSource.
	PlanSource PlanSource
	PlanDigest string
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

func (e *Engine) run(ctx context.Context, taskID, task string, how Provenance) {
	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.TaskCreated, task, nil))
	// Recorded before anything reads it, and never written again. The mode
	// event used to be the only place provenance went, so it was announced and
	// then dropped: nothing downstream could tell a task a human asked for from
	// one an AI submitted with identical wording.
	e.recordObjective(taskID, Objective{Text: task, Provenance: how})
	e.announceMode(taskID, governedMode(how))
	e.execute(ctx, taskID, task)
}

// objective returns what this task was asked to do, and what established that
// anyone asked. A task nothing recorded reads as unestablished, which is the
// honest answer rather than a missing one.
func (e *Engine) objective(taskID string) Objective {
	e.mu.Lock()
	defer e.mu.Unlock()
	if o, ok := e.objectives[taskID]; ok {
		return o
	}
	return Objective{Provenance: SubmittedUnattended}
}

// recordObjective stores the request and its provenance at submission.
func (e *Engine) recordObjective(taskID string, o Objective) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.objectives == nil {
		e.objectives = make(map[string]Objective)
	}
	e.objectives[taskID] = o
}

// observes reports whether a task entered through the observation lane.
func (e *Engine) observes(taskID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.observing[taskID]
}

// markObserving records the lane before the run starts.
func (e *Engine) markObserving(taskID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.observing == nil {
		e.observing = make(map[string]bool)
	}
	e.observing[taskID] = true
}

// execute is the governed run itself, separated from how it was entered.
//
// A task resumed after a deferred authority decision re-enters here rather than
// through run: it was created and announced once already, and announcing it a
// second time as a fresh human request would overwrite its real provenance.
func (e *Engine) execute(ctx context.Context, taskID, task string) {
	fail := func(err error) {
		// A deferred authority decision has already recorded itself, with the
		// question attached. Reporting it again as a failure or a stop would
		// describe the same moment three ways, and two of them would be wrong:
		// nothing failed, and the human did not withdraw from the work — they
		// declined to answer one question about it.
		if errors.Is(err, errAuthorityDeferred) {
			return
		}
		// A stopped run is not a failed one, and recording it as failure would
		// teach the behavioural record that this task shape breaks. The human
		// withdrew; nothing was proved about the work. The outcome is still
		// reported, on a context the cancellation cannot reach, because a run
		// that goes silent when stopped leaves no account of why it ended.
		if ctx.Err() != nil {
			const note = "stopped by the human; the candidate is left as it stands"
			e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.WorkflowStopped, note, nil))
			e.reportOutcome(context.WithoutCancel(ctx), "stopped", task, note)
			return
		}
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
	if ws, derr := sensei.DecodeWorkspaceStatus(workspaceStatus); derr == nil {
		e.bindGraph(taskID, ws)
	}

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
	start, err := certifyStartForLane(workspaceStatus, preflight, repositoryHead(ctx, e.Repo), e.observes(taskID))
	if err != nil {
		e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status, err.Error(), preflight.Structured))
		e.reportOutcome(ctx, "blocked", task, err.Error())
		fail(err)
		return
	}

	// The observation lane leaves here, before the change lifecycle begins.
	//
	// It used to run the whole governed path and stop near the end, which meant
	// an observation established a candidate identity, wrote
	// .sensei-code/candidates/<task>.json, and left it unresolved -- and an
	// architect that answered "reply" exited through the completion branch
	// above, reporting a read-only run as a completed change.
	//
	// Both were the same defect: the lane was a guard sprinkled into the change
	// workflow rather than a path of its own. Branching here removes both by
	// construction. Nothing below this line can run for an observation, so
	// nothing below it needs to know the lane exists.
	if e.observes(taskID) {
		if err := e.observe(ctx, sc, start, taskID, task, workspaceStatus); err != nil {
			e.reportOutcome(ctx, "blocked", task, err.Error())
			fail(err)
		}
		return
	}

	// The candidate base is pinned here, immediately after the start gate, and
	// not later when the worktree is created.
	//
	// The workflow writes to its own repository between those two points: a
	// Level-3 resolution is persisted into this repository's awareness corpus,
	// which makes the tree dirty. Establishing the base afterwards then refused
	// the run for uncommitted changes the run had itself produced. The gate was
	// right and the ordering was wrong.
	//
	// Pinning it here is also what P0.5 actually meant. The earliest honest
	// moment is the one where Sensei has just certified the workspace and the
	// tree has just been observed clean; every later moment is a moment the
	// system may have perturbed itself.
	identity, err := candidate.Establish(
		e.Repo.Root, taskID, start.Domain(),
		e.Repo.WorktreePath(taskID), e.Repo.WorktreeBranch(taskID),
		repoHead{ctx: ctx, repo: e.Repo}, time.Now(),
	)
	if err != nil {
		e.reportOutcome(ctx, "blocked", task, err.Error())
		fail(err)
		return
	}
	if bound, bindErr := identity.BindGraph(e.Repo.Root, start.GraphBuildCommit(), start.SourceRepoCommit()); bindErr == nil {
		identity = bound
	}

	conversation := e.conversationSoFar(task, 40)
	domain := sensei.RepositoryDomain(workspaceStatus)
	// Assembled before the architect is asked anything, and emitted, so what it
	// was given is a record rather than an assumption.
	retrievedEvidence, repositoryEvidence, standingContext, recordedHistory, consulted := e.architecturalContext(ctx, sc, domain, task)
	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.ContextConsulted, consulted.Render(), consulted))
	var decision architectureDecision
	if supplied, ok := e.suppliedPlan(taskID); ok {
		// The bound was handed in. It is routed exactly as an architect's
		// would be; it is not authored here and cannot be revised here.
		decision, err = e.resolveSuppliedPlan(ctx, sc, start, taskID, task, supplied)
	} else {
		decision, err = e.resolveArchitecture(ctx, sc, start, taskID, task, architecturePrompt(
			e.Repo.Root, domain, config.DisplayName(e.Config.Architect.Name), task, conversation,
			firstText(workspaceStatus), firstText(preflight),
			retrievedEvidence, repositoryEvidence, standingContext, recordedHistory))
	}
	if err != nil {
		fail(err)
		return
	}
	if decision.Decision == "reply" {
		e.emit(event.New(e.SessionID, taskID, event.SourceArchitect, event.ArchitectSpoke, decision.Message, decision))
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.WorkflowCompleted, "", nil))
		return
	}
	// The plan is published as information, not as a gate.
	//
	// There used to be a rendezvous here: "Implement this plan? 1/2". It asked
	// the human to authorize what they had already authorized by typing /run,
	// about a plan the authority router had already ruled on. It produced no
	// evidence, made no decision the router had not made, and recorded nothing
	// durable — the definition of ceremony. Every remaining interruption in a
	// governed run comes from the router naming a Level-3 condition, or from
	// the publication rendezvous, which is a genuinely different decision.
	//
	// The human's "no" did not disappear with it: a running task can be stopped
	// (see Stop), which costs nothing when unused, where a mandatory prompt
	// taxed every run.
	e.emit(event.New(e.SessionID, taskID, planEventSource(e.planSource(taskID)), event.PlanProposed,
		planSummaryFrom(decision, e.planSource(taskID), e.planDigest(taskID)),
		proposedPlan{architectureDecision: decision, PlanSource: e.planSource(taskID), PlanDigest: e.planDigest(taskID)}))
	plan := decision.Plan
	tc := taskContext{
		Task:            task,
		Conversation:    conversation,
		WorkspaceStatus: firstText(workspaceStatus),
		Preflight:       firstText(preflight),
		Rationale:       decision.Summary,
		Files:           decision.Files,
		Steps:           decision.Steps,
		Domain:          sensei.RepositoryDomain(workspaceStatus),
		Mode:            planMode(decision.Mode),
		Consequences:    decision.Consequences,
		Invariants:      decision.Invariants,
		Prospective:     decision.ProspectiveSurfaces,
		PlanSource:      e.planSource(taskID),
		PlanDigest:      e.planDigest(taskID),
		Identity:        identity,
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

// offerPullRequest asks whether to publish an accepted candidate. Publication
// is human-owned, so it is gated twice: the configuration must grant push at
// all, and the human must say yes to this particular change. Sensei Code opens
// the pull request and stops there; merging is never its decision.
func (e *Engine) offerPullRequest(ctx context.Context, taskID string, tc *taskContext, workspace, worker string) publication {
	if !e.Config.Permissions.Push {
		e.emit(event.New(e.SessionID, taskID, event.SourceGit, event.Status,
			"no pull request offered: "+publish.ErrPushNotGranted.Error(), nil))
		return publication{State: notOffered}
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
	// The error was discarded here, which is how a deferral became a
	// completion. Esc emits WorkflowAwaitingAuthority -- "the question stands"
	// -- and the caller then recorded the run as finished, which
	// FindInterrupted reads as terminal, so /resume answered "nothing to
	// resume" to a question the interface had just promised to ask again.
	switch {
	case errors.Is(err, errAuthorityDeferred):
		return publication{State: deferred}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return publication{State: stopped, Err: err}
	case err != nil:
		return publication{State: failed, Err: err}
	case !strings.HasPrefix(choice, "1:"):
		return publication{State: declined}
	}
	done, err := publish.Open(ctx, publish.Request{
		Workspace: workspace,
		Branch:    e.Repo.WorktreeBranch(taskID),
		Base:      e.Config.Workflow.PublishBase,
		Title:     tc.Task,
		Report:    tc.Report,
		// Exactly the paths the accepted candidate changed, as snapshotted at
		// its last audit. Nothing else in the worktree is published.
		Paths: tc.EvidenceSnapshot.ChangedPaths,
	}, e.Config.Permissions.Push, e.Config.Permissions.LocalCommit)
	if err != nil {
		// What already reached the remote is stated. A failed publication that
		// pushed a branch has changed the world, and reporting only the error
		// leaves that change recorded nowhere.
		detail := "pull request not opened: " + err.Error()
		if effects := done.Effects(); effects != "" {
			detail += "\n" + effects
		}
		e.emit(event.New(e.SessionID, taskID, event.SourceGit, event.Status, detail, map[string]any{
			"committed": done.Committed, "pushed": done.Pushed,
		}))
		return publication{State: failed, Err: err, Result: done}
	}
	e.emit(event.New(e.SessionID, taskID, event.SourceGit, event.PullRequestOpened,
		"pull request opened, not merged and not admitted: "+done.URL, map[string]string{"url": done.URL}))
	return publication{State: opened, Result: done}
}

// publication is how far the publication rendezvous got, and why it stopped.
//
// It is returned rather than swallowed because the caller's next three acts --
// which disposition to record, whether to emit a terminal event, and which one
// -- all depend on it. When offerPullRequest returned nothing the caller could
// only assume the one outcome that is wrong in four of the six cases.
type publication struct {
	State  publicationState
	Result publish.Result
	Err    error
}

type publicationState string

const (
	// notOffered: the repository does not grant push, so no question was asked.
	notOffered publicationState = "not_offered"
	// declined: the human chose to leave the candidate local.
	declined publicationState = "declined"
	// deferred: the human left the question standing. NOT an answer, and not a
	// finished run.
	deferred publicationState = "deferred"
	// stopped: the human withdrew attention while the question was open.
	stopped publicationState = "stopped"
	// opened: a pull request exists.
	opened publicationState = "opened"
	// failed: publication was attempted and did not complete.
	failed publicationState = "failed"
)

// Settled reports whether the run may record a terminal outcome. A question the
// human left standing has settled nothing, and neither has a run they stopped.
func (p publication) Settled() bool {
	return p.State != deferred && p.State != stopped
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

// decisionAuthority reports who actually authorized this work.
//
// The answer is read from what happened, not asserted. If a Level-3 condition
// reached the human during this task, the record says so and names the
// condition and the option they chose. If none did, the work was decided
// architecturally by the architect inside a region Sensei certified, and the
// human's contribution was authorizing the task — which is what /run is. Saying
// "accepted by the human" for that case, as this once did unconditionally,
// attributes to a person a judgement they never made.
func (e *Engine) decisionAuthority(taskID string, start certifiedStart) decision.Authority {
	certified := strings.TrimSpace(start.Domain())
	if commit := strings.TrimSpace(start.GraphBuildCommit()); commit != "" {
		certified = strings.TrimSpace(certified + " @ " + commit)
	}
	if answered := e.authorityDecisions(taskID); len(answered) != 0 {
		// The last answer is the one the plan finally rested on: an earlier
		// condition that was answered and then superseded did not authorize
		// the work that shipped.
		last := answered[len(answered)-1]
		return decision.Authority{
			Owner:       decision.Human,
			CertifiedBy: certified,
			Condition:   last.Condition,
			Resolution:  last.Chosen,
		}
	}
	// Who decided and what the human granted are read from what the task
	// recorded, not from which agent is configured. A supplied plan was decided
	// by nobody in the run, and a headless submission was granted by nobody a
	// person is established to be.
	decidedBy := config.DisplayName(e.Config.Architect.Name)
	if p, ok := e.suppliedPlan(taskID); ok {
		decidedBy = "supplied plan sha256 " + p.Digest + " (not architect-produced)"
	}
	grant := "none established: " + string(e.objective(taskID).Provenance)
	if e.objective(taskID).HumanAuthorized() {
		grant = "task execution via /run"
	}
	return decision.Authority{
		Owner:       decision.Architectural,
		CertifiedBy: certified,
		DecidedBy:   decidedBy,
		HumanGrant:  grant,
	}
}

// recordDecision writes the accepted plan into Sensei as an architectural
// decision, so the reason this work was authorized outlives the session and any
// agent can read it later. A decision Sensei would refuse is reported, never
// padded with invented links to make it pass.
func (e *Engine) recordDecision(ctx context.Context, taskID string, tc *taskContext, start certifiedStart, changed []string) {
	record := decision.Record{
		Title:        strings.TrimSpace(tc.Rationale),
		Rationale:    tc.Task,
		Authority:    e.decisionAuthority(taskID, start),
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

// proposedPlan is the PlanProposed payload: the decision plus who authored it.
//
// The source lives on the record and not on architectureDecision itself, so a
// supplied file cannot carry one (ParseSuppliedPlan refuses unknown fields)
// while the engine-written record always does. It is what a resume reads to
// re-establish the bound, so a restart cannot turn a supplied plan back into
// one the architect may revise.
type proposedPlan struct {
	architectureDecision
	PlanSource PlanSource `json:"plan_source,omitempty"`
	PlanDigest string     `json:"plan_digest,omitempty"`
}

// planEventSource is who the PlanProposed event is attributed to. A supplied
// plan is announced by the system that received it, not by an architect that
// never spoke.
func planEventSource(source PlanSource) event.Source {
	if source == PlanSupplied {
		return event.SourceSystem
	}
	return event.SourceArchitect
}

// planSummaryFrom is planSummary with the plan's source stated first when it
// was not the architect. The summary is what a resumed task carries as its
// plan, so the label has to be in the text itself or a resume drops it.
func planSummaryFrom(d architectureDecision, source PlanSource, digest string) string {
	if source != PlanSupplied {
		return planSummary(d)
	}
	return strings.TrimRight("Supplied plan (not architect-produced; sha256 "+digest+")\n"+planSummary(d), "\n")
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
	// previousReportRevision is the same guard for a read-only run, where the
	// artifact is findings rather than a diff. Without it a worker that returns
	// the same report every cycle spends the whole budget re-asserting it.
	var previousReportRevision string

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
		prompt := implementationPrompt(*tc, plan, feedback, cycle, guidance, joinGrants(renderProspectiveGrants(e.prospectiveGrants(taskID)), renderTestEditGrants(e.testEditGrants(taskID))))
		impl := agent.CLI{Name: worker.Name, Label: config.DisplayName(worker.Name), Command: worker.Command, Args: worker.Args, NoGraph: !worker.ConsumesGraph(), Source: sourceFor(worker.Name), SessionID: e.SessionID, Env: guardEnv, UnsetEnv: provider.SessionOnlyEnv}
		// The worker's own text is the artifact of a read-only plan. Discarding
		// it left an inspection with nothing to show but a transcript nobody
		// had judged.
		result, err := impl.Run(ctx, agent.Request{Role: roles.Implementer, TaskID: taskID, Workspace: workspace, Prompt: prompt, Graph: e.graphFor(taskID)}, e.emit)
		if err != nil {
			return false, plan, lastReview, lastAudit, fmt.Errorf("implementor cycle %d: %w", cycle, err)
		}
		report := strings.TrimSpace(result.Text)

		candidate := gitx.Repo{Root: workspace}
		capture, err := candidate.CandidateCapture(ctx, tc.Identity.BaseSHA, tc.Files)
		if err != nil {
			return false, plan, lastReview, lastAudit, err
		}
		diff := capture.Diff
		// Every refusal at the boundary is REPRESENTED, with the path, size
		// and reason, before anything downstream sees the candidate (#89).
		for _, a := range capture.Excluded {
			e.emit(event.New(e.SessionID, taskID, event.SourceGit, event.CandidateArtifactExcluded,
				fmt.Sprintf("excluded from the candidate: %s (%s, %d bytes) — %s", a.Path, a.Class, a.Size, a.Reason),
				map[string]string{"path": a.Path, "class": a.Class, "size": fmt.Sprint(a.Size), "reason": a.Reason}))
		}
		if len(capture.Binaries) != 0 {
			e.emit(event.New(e.SessionID, taskID, event.SourceGit, event.Status,
				"binary members kept as metadata, not transported: "+gitx.Describe(capture.Binaries), nil))
		}
		if strings.TrimSpace(diff) == "" {
			// For read-only work an empty diff is the expected shape, but it is
			// not the result. The result is the report, and "the diff was
			// empty" is a fact about what the worker did not do -- accepting on
			// it alone made an inspection self-certifying: a worker that
			// produced nothing, or produced something unsupported, passed on
			// exactly the same evidence as one that did the work.
			if tc.Mode == ModeInspect {
				if report == "" {
					return false, plan, lastReview, lastAudit, errors.New(
						"the read-only plan produced no findings: the worker changed nothing and reported nothing")
				}
				e.emit(event.New(e.SessionID, taskID, sourceFor(worker.Name), event.InspectionReported, report, nil))

				revision := reportRevision(report)
				if revision == previousReportRevision {
					return false, plan, lastReview, lastAudit, fmt.Errorf(
						"the findings did not change between review cycles: %s returned an identical report after being asked to revise. "+
							"The last review asked for: %s", config.DisplayName(worker.Name), oneLine(lastReview))
				}
				previousReportRevision = revision

				binding := roles.Binding{TaskID: taskID, BaseSHA: tc.Identity.BaseSHA, CandidateDigest: revision}
				assignment, err := roles.Assign(roles.Reviewer, e.reviewCapabilities(), worker.Name)
				if err != nil {
					return false, plan, lastReview, lastAudit, fmt.Errorf("%w: %v", roles.ErrNoIndependentReviewer, err)
				}
				if err := e.policyFor(taskID).Check(worker.Name, assignment); err != nil {
					return false, plan, lastReview, lastAudit, err
				}
				review, err := e.resolveReview(ctx, taskID, assignment,
					inspectionPacket(*tc, binding, start, plan, report), worker.Name)
				if err != nil {
					return false, plan, lastReview, lastAudit, err
				}
				lastReview = review.Summary
				tc.EvidenceSnapshot = taskstate.Evidence{
					ReportBytes:  len(report),
					AuditVerdict: string(review.Decision),
					AuditDetail:  review.Summary,
				}
				switch review.Decision {
				case roles.Accept:
					e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status,
						"read-only plan completed: the findings were reviewed independently and accepted", nil))
					return true, plan, lastReview, lastAudit, nil
				case roles.Escalate:
					// The same routing as a change: the reviewer reaches the
					// architect, never the human. Failing the run here instead
					// would let a reviewer end a task by raising a question
					// nobody was given the chance to answer.
					revised, err := e.resolveArchitectureForRevision(ctx, sc, start, taskID, task, escalationPrompt(task, plan, report, review), "the reviewer escalated: "+oneLine(review.Summary))
					if err != nil {
						return false, plan, lastReview, lastAudit, err
					}
					if strings.TrimSpace(revised.Plan) == "" {
						return false, plan, lastReview, lastAudit, errors.New("architect did not return a revised bounded plan")
					}
					e.recordReconciliation(taskID, binding, roles.Reconciliation{
						Disputed: "the reviewer raised an architectural boundary about the findings: " + review.Summary,
						Inputs: []roles.Claim{
							{Agent: review.Provenance.Provider, Role: roles.Reviewer, Position: review.Summary},
							{Agent: config.DisplayName(e.Config.Architect.Name), Role: roles.Architect, Position: revised.Summary},
						},
						Canonical: reconciliationEvidence("", "", revised),
						Decision:  revised.Plan,
						Authority: roles.ArchitectAuthority,
						Remaining: revised.Consequences,
					})
					e.emit(event.New(e.SessionID, taskID, event.SourceArchitect, event.Status, revised.Summary, revised))
					plan = revised.Plan
					feedback = "The architect resolved the review escalation. Inspect again under the revised plan."
					continue
				default:
					// Send the objections back and inspect again. The findings
					// are the deliverable, so an unsupported one is revisable
					// in exactly the way an unsound change is.
					feedback = review.Instruction()
					continue
				}
			}
			return false, plan, lastReview, lastAudit, errors.New("implementor produced no candidate diff")
		}
		// A read-only plan that changed something is out of scope, and saying so
		// is more useful than reviewing the change: the worker was asked to
		// report and it edited instead.
		if tc.Mode == ModeInspect {
			return false, plan, lastReview, lastAudit, fmt.Errorf(
				"the plan was read-only and the candidate changed %d file(s): %s",
				len(changedPaths(diff)), strings.Join(changedPaths(diff), ", "))
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

		// Post-creation inspection of every declared prospective surface
		// (sensei#312). The authorization was for a shape; a candidate whose
		// created file has another shape is refuted here, before any review
		// is asked and with no retry: the authorized shape was not the shape
		// produced, and nothing downstream may reinterpret that.
		if len(tc.Prospective) != 0 {
			facts := map[string]prospectiveFacts{}
			for _, g := range e.prospectiveGrants(taskID) {
				facts[g.Anchor.File] = g.Facts
			}
			if err := inspectProspectiveSurfaces(diff, tc.Prospective, facts); err != nil {
				return false, plan, lastReview, lastAudit, err
			}
		}
		// Post-edit inspection of every granted existing test (M2.2): the
		// candidate's file against the exact grant, before any review, with
		// no retry -- the same discipline as a prospective refutation.
		if edits := e.testEditGrants(taskID); len(edits) != 0 {
			if err := inspectTestEdits(diff, edits, func(p string) ([]byte, error) { return os.ReadFile(filepath.Join(workspace, p)) }); err != nil {
				return false, plan, lastReview, lastAudit, err
			}
		}

		auditArgs := map[string]any{"diff": diff, "task": task}
		// Scope the audit to the domain the start gate certified, so the audit
		// evaluates this candidate against this repository's rules rather than
		// whatever the graph resolves by default.
		if domain := start.Domain(); domain != "" {
			auditArgs["domain"] = domain
		}
		// expected_head is the commit this candidate was cut from, and Sensei
		// needs it to audit a modification at all.
		//
		// It was deliberately omitted, on a rationale that was true when written:
		// the audit compared expected_head against the graph's own authority
		// commit, which identifies the rule snapshot rather than the repository,
		// so a sensei-code commit could never equal it and pinning one produced
		// cannot_verify on every audit. Omitting it produced cannot_verify too,
		// through the other door -- base content is read only from a
		// caller-pinned commit, so without one a modify hunk cannot be
		// reconstructed and the audit reports repository_context_unavailable.
		// Both roads ended in the same place, and no candidate that modified an
		// existing file could ever be verified.
		//
		// Sensei removed the false coupling (sensei 7bf987d4): the two name
		// different things and are no longer compared. The remaining rules are
		// the real ones and both still bind -- a modified file requires a
		// caller-pinned base, and an authoritative graph requires an exact
		// source/build commit.
		//
		// Measured against sensei b9ebca0c on the same diff: without this field
		// cannot_verify/repository_context_unavailable; with it, pass/available.
		//
		// It is the CANDIDATE's base, not the repository's head. Once a worktree
		// exists those differ, and auditing a candidate against a commit it was
		// not cut from would reconstruct the wrong pre-change bytes -- which is
		// worse than not auditing, because it would look like it worked.
		if base := strings.TrimSpace(tc.Identity.BaseSHA); base != "" {
			auditArgs["expected_head"] = base
		}
		audit, err := sc.CallTool("awareness_audit_diff", auditArgs)
		if err != nil {
			// No verdict was obtained for this candidate. That is structural,
			// not a worker failure: the transport refused the payload, or the
			// tool errored, and another executor handed the same candidate
			// would fail the same way. Returning an ordinary error here sent
			// it down the handoff path anyway (#89, second review).
			reason := auditCallFailure(err)
			e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.CandidateNotAuditable, reason, nil))
			return false, plan, lastReview, lastAudit, structuralFailure(reason)
		}
		lastAudit = firstText(audit)
		e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.CandidateAudited, lastAudit, audit.Structured))

		// The audit is decoded before the reviewer is consulted. The reviewer
		// still receives the prose, because prose is what a model reasons over,
		// but the verdict that governs acceptance is the structured one.
		verdict, err := sensei.DecodeDiffAudit(audit)
		if err != nil {
			reason := auditCallFailure(err)
			e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.CandidateNotAuditable, reason, audit.Structured))
			return false, plan, lastReview, lastAudit, structuralFailure(reason)
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
		// A structural refusal is about the payload, not the change. No
		// reviewer can judge it and no further implementor can fix it without
		// a new candidate, so it is named here and the run ends here (#89).
		if reason := structuralAuditFailure(verdict); reason != "" {
			e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.CandidateNotAuditable, reason, audit.Structured))
			return false, plan, lastReview, lastAudit, structuralFailure(reason)
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

		// The candidate revision is the identity every cross-agent artifact from
		// here on is about. It is computed from the diff rather than taken from
		// the audit, so a review is still bound to what it read on a run where
		// Sensei could not audit at all.
		binding := roles.Binding{TaskID: taskID, BaseSHA: tc.Identity.BaseSHA, CandidateDigest: candidateRevision(diff)}
		policy := e.policyFor(taskID)

		// The implementer is excluded by construction, not by instruction. An
		// author reviewing its own work has already decided the question, and
		// its agreement carries no information about whether the work is right.
		assignment, err := roles.Assign(roles.Reviewer, e.reviewCapabilities(), worker.Name)
		if err != nil {
			return false, plan, lastReview, lastAudit, fmt.Errorf("%w: %v", roles.ErrNoIndependentReviewer, err)
		}
		if err := policy.Check(worker.Name, assignment); err != nil {
			return false, plan, lastReview, lastAudit, err
		}

		// Stage 1 of the Level-1 routine tier: classify, report, grant nothing.
		//
		// The classification is computed on every governed run and skips no
		// step, because the question it answers -- would this have qualified,
		// and if not which condition stopped it -- can only be settled from
		// real runs. Deciding it from intuition is how a fast path acquires
		// conditions that were never true.
		e.classifyForDarkRun(sc, start, taskID, tc, diff)

		review, err := e.resolveReview(ctx, taskID, assignment,
			reviewPacket(*tc, binding, start, plan, diff, lastAudit, evidence.Render()), worker.Name)
		if err != nil {
			return false, plan, lastReview, lastAudit, err
		}
		lastReview = review.Summary
		switch review.Decision {
		case roles.Accept:
			// Sensei owns this transition. A reviewer that accepts over a
			// refusal does not conclude the candidate; the refusal becomes the
			// next revision instruction instead.
			if judged := judgeCandidate(string(review.Decision), verdict); !judged.Accepted {
				e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status,
					"reviewer accepted but Sensei refused; the refusal governs: "+judged.Refusal, audit.Structured))
				// A refusal the worker cannot act on must stop the loop rather
				// than drive it. An audit that could not run objects to the
				// environment, not to the candidate, so sending it back produces
				// a byte-identical diff and consumes the next cycle for nothing.
				if unactionable := evidence.Unactionable(); len(unactionable) != 0 && len(evidence.CandidateFailures()) == 0 {
					// The only thing blocking acceptance is something no edit to
					// the candidate can reach. Asking for a revision would be
					// asking for the impossible, politely, until the cycles run
					// out.
					names := make([]string, 0, len(unactionable))
					for _, u := range unactionable {
						names = append(names, string(u.Outcome)+" "+u.Command+": "+u.Detail)
					}
					return false, plan, lastReview, lastAudit, fmt.Errorf(
						"validation could not be completed for reasons outside the candidate, so no revision would help: %s",
						strings.Join(names, "; "))
				}
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
			e.recordDecision(ctx, taskID, tc, start, changedPaths(diff))
			return true, plan, lastReview, lastAudit, nil
		case roles.Revise:
			feedback = review.Instruction()
			e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status, "review requested bounded revision; continuing autonomously", map[string]int{"cycle": cycle}))
		case roles.Escalate:
			// The reviewer reaches the architect, never the human. A reviewer
			// that could interrupt a person directly would let one nervous
			// model manufacture a Level-3 event, which is the mirror image of
			// letting a confident one skip past it.
			revised, err := e.resolveArchitectureForRevision(ctx, sc, start, taskID, task, escalationPrompt(task, plan, lastAudit, review), "the reviewer escalated: "+oneLine(review.Summary))
			if err != nil {
				return false, plan, lastReview, lastAudit, err
			}
			if strings.TrimSpace(revised.Plan) == "" {
				return false, plan, lastReview, lastAudit, errors.New("architect did not return a revised bounded plan")
			}
			e.recordReconciliation(taskID, binding, roles.Reconciliation{
				Disputed: "the reviewer raised an architectural boundary: " + review.Summary,
				Inputs: []roles.Claim{
					{Agent: review.Provenance.Provider, Role: roles.Reviewer, Position: review.Summary},
					{Agent: config.DisplayName(e.Config.Architect.Name), Role: roles.Architect, Position: revised.Summary},
				},
				Canonical: reconciliationEvidence(lastAudit, evidence.Render(), revised),
				Decision:  revised.Plan,
				Authority: roles.ArchitectAuthority,
				Remaining: revised.Consequences,
			})
			e.emit(event.New(e.SessionID, taskID, event.SourceArchitect, event.Status, revised.Summary, revised))
			plan = revised.Plan
			feedback = "The architect resolved the review escalation. Reconcile the current candidate with the revised plan."
		default:
			return false, plan, lastReview, lastAudit, fmt.Errorf("unsupported review decision %q", review.Decision)
		}
	}
	return false, plan, lastReview, lastAudit, fmt.Errorf("candidate did not converge after %d review cycles", e.Config.Workflow.ReviewCycles)
}

// planMode normalises the architect's declaration.
//
// An absent or unrecognised value is modify. That is the conservative reading:
// treating an unknown mode as inspect would let a malformed field turn a
// change request into a run that accepts producing nothing.
func planMode(declared string) string {
	if strings.EqualFold(strings.TrimSpace(declared), ModeInspect) {
		return ModeInspect
	}
	return ModeModify
}

// ModeInspect is a plan that reads and reports and changes nothing.
// ModeModify is a plan that edits the repository. Absent means modify, so a
// provider that has never heard of this field behaves exactly as before.
const (
	ModeInspect = "inspect"
	ModeModify  = "modify"
)

type architectureDecision struct {
	Decision     string   `json:"decision"`
	Summary      string   `json:"summary"`
	Message      string   `json:"message,omitempty"`
	Steps        []string `json:"steps,omitempty"`
	Consequences string   `json:"consequences,omitempty"`
	Files        []string `json:"files,omitempty"`
	// Mode says whether this plan changes the repository. It borrows Sensei's
	// own vocabulary -- inspect or modify -- rather than inventing a third.
	//
	// It exists because an audit had nowhere to go. "Audit sensei-code" produced
	// a correct read-only plan, both implementors carried it out correctly, and
	// the run failed with "no bounded implementor produced an acceptable
	// candidate" -- because the loop reads an empty diff as a worker that failed
	// to produce a change. For read-only work an empty diff is the result.
	//
	// It is declared rather than inferred. Files lists what a plan touches and an
	// audit touches many files without changing one, so the file list cannot
	// answer this, and inferring it from the summary would be a governance
	// decision resting on prose.
	Mode           string             `json:"mode,omitempty"`
	Invariants     []string           `json:"related_invariants,omitempty"`
	Plan           string             `json:"plan"`
	HumanQuestion  string             `json:"human_question,omitempty"`
	Recommendation string             `json:"recommendation,omitempty"`
	Options        []authority.Option `json:"options,omitempty"`
	// Claims are the factual premises the plan rests on. They are evidence the
	// router reads, not a verdict: a premise the architect marks "inference" is
	// the model telling us nothing checked it.
	Claims []Claim `json:"claims,omitempty"`
	// ProposedRecipe is a checkable QUESTION a closure round found worth asking
	// about the region it investigated. It is not knowledge and it establishes
	// nothing.
	//
	// The architect stage is read-only and stays that way: it PROPOSES here and
	// the engine writes, so provenance is stamped by something the agent does
	// not control. It cannot cover the task that proposed it, and it becomes
	// coverage only if `sensei derive` answers it against a later world.
	ProposedRecipe *derived.Recipe `json:"proposed_recipe,omitempty"`
	// ProspectiveSurfaces declares the files this plan will CREATE, so a file
	// absent at the pinned world can be covered by prospective authority
	// (sensei#312). It is a claim the predicate in prospective.go checks
	// against the covering surface's bytes at the pinned world; an undeclared
	// new file is uncovered exactly as before.
	ProspectiveSurfaces []ProspectiveSurface `json:"prospective_surfaces,omitempty"`
	// PremiseResolutions are the closure round's answers to the premise
	// receipts it was asked about. See premise.go.
	PremiseResolutions []PremiseResolution `json:"premise_resolutions,omitempty"`
}

// reviewDecision is the reviewer's wire contract. It is deliberately separate
// from roles.ReviewVerdict: this is what a model returned, and the verdict is
// what the workflow is willing to treat as a review. Provenance is attached
// here rather than asked for, because an artifact that states its own identity
// can state a convenient one.
type reviewDecision struct {
	Decision     string          `json:"decision"`
	Summary      string          `json:"summary"`
	Instructions string          `json:"instructions,omitempty"`
	Findings     []roles.Finding `json:"findings,omitempty"`
}

func (e *Engine) resolveArchitecture(ctx context.Context, sc *sensei.Client, start certifiedStart, taskID, task, prompt string) (architectureDecision, error) {
	return e.resolveArchitectureIn(ctx, sc, start, taskID, task, prompt, e.Repo.Root)
}

// resolveArchitectureForRevision is resolveArchitecture for a plan that already exists and needs
// revising: the review-escalation paths. It refuses when the plan was supplied,
// because the architect revising a supplied bound would put two authors under
// the one label the receipt carries.
func (e *Engine) resolveArchitectureForRevision(ctx context.Context, sc *sensei.Client, start certifiedStart, taskID, task, prompt, why string) (architectureDecision, error) {
	if _, ok := e.suppliedPlan(taskID); ok {
		return architectureDecision{}, errSuppliedPlanCannotBeRevised(why)
	}
	return e.resolveArchitecture(ctx, sc, start, taskID, task, prompt)
}

// resolveSuppliedPlan routes a supplied plan through the same authority
// boundary an architect's plan crosses in resolveArchitectureIn's "proceed"
// branch, with the same router and the same recorded routing.
//
// The branches that re-prompt the architect there fail closed here. A bounded
// knowledge gap is closed by asking the architect to investigate; a human
// answer is folded into the plan by asking the architect to re-plan under it.
// Neither is available for a plan nobody in the run authored, and doing either
// with the architect would make the supplied bound a revised one. The one
// human path kept is the answer that authorises the plan AS SUPPLIED: the run
// asks, and proceeds only if the answer covers this exact plan.
func (e *Engine) resolveSuppliedPlan(ctx context.Context, sc *sensei.Client, start certifiedStart, taskID, task string, supplied SuppliedPlan) (architectureDecision, error) {
	d := supplied.decision
	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status,
		"the plan was supplied with the task (sha256 "+supplied.Digest+"); the architect is not consulted for it, and it is routed as any plan is", nil))
	routing, scoped, err := e.routePlan(ctx, sc, start, taskID, task, d)
	if err != nil {
		return architectureDecision{}, err
	}
	e.setRouting(taskID, roles.PolicyFor(routing.Blast, routing.Gate), scoped, d.Claims, d.Files)
	switch {
	case routing.Route == RouteCannotEstablish:
		return architectureDecision{}, fmt.Errorf("cannot establish authority for this plan: %s", routing.Condition)
	case routing.ClosesGap():
		return architectureDecision{}, errSuppliedPlanCannotBeRevised("a bounded knowledge gap must be closed first: " + routing.Condition)
	case routing.RequiresHuman():
		if authorized, asked := e.applyAnsweredCondition(taskID, routing.Condition, d.Files...); asked {
			if !authorized {
				return architectureDecision{}, fmt.Errorf("the human declined this architectural change and the plan still requires it: %s", routing.Condition)
			}
			e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status,
				"proceeding on the human's earlier authorization for: "+routing.Condition, nil))
			return d, nil
		}
		e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status, escalationCondition(routing), nil))
		if _, err := e.awaitHuman(ctx, sc, start, taskID, d, routing.Condition); err != nil {
			return architectureDecision{}, err
		}
		// The answer is read back through the same record an architect's
		// re-plan would consult, so what authorises the run is the recorded
		// resolution and not the option string the prompt returned.
		if authorized, _ := e.applyAnsweredCondition(taskID, routing.Condition, d.Files...); !authorized {
			return architectureDecision{}, errSuppliedPlanCannotBeRevised("the human's answer did not authorise the plan as supplied: " + routing.Condition)
		}
		return d, nil
	}
	return d, nil
}

// resolveArchitectureIn is resolveArchitecture with an explicit working
// directory, so the observation lane can run the architect somewhere the
// governed checkout is not.
func (e *Engine) resolveArchitectureIn(ctx context.Context, sc *sensei.Client, start certifiedStart, taskID, task, prompt, workspace string) (architectureDecision, error) {
	architect := agent.CLI{Name: e.Config.Architect.Name, Label: config.DisplayName(e.Config.Architect.Name), Command: e.Config.Architect.Command, Args: e.Config.Architect.Args, NoGraph: !e.Config.Architect.ConsumesGraph(), Source: event.SourceArchitect, SessionID: e.SessionID, UnsetEnv: provider.SessionOnlyEnv}
	var lastErr error
	// rounds bounds the whole resolution, which attempt does not.
	//
	// attempt budgets malformed JSON, and four paths legitimately reset it to
	// start a fresh question: a human answer, and an escalation into a region
	// Sensei certifies. The certified path needs no person, rebuilds the prompt
	// by nesting the previous one inside certifiedResolutionPrompt, and comes
	// straight back here -- so an architect that keeps escalating loops with no
	// ceiling, spending a provider turn and growing the prompt every round,
	// until the context is cancelled.
	//
	// The limit is generous because a real run resolves in one or two rounds
	// and the human-answered paths are already bounded by a person's patience.
	// It exists to end a loop, not to ration ordinary work.
	rounds := 0
	const maxRounds = 8
	newRound := func(reason string) error {
		rounds++
		if rounds > maxRounds {
			return fmt.Errorf(
				"the architect did not settle after %d resolution rounds; the last was %s. "+
					"Each round consumes a provider turn and nests the previous prompt, so this is stopped rather than left to run",
				maxRounds, reason)
		}
		return nil
	}
	for attempt := 1; attempt <= 2; attempt++ {
		p := prompt
		if attempt > 1 {
			p += "\n\nYour previous response was not valid bounded JSON. Return ONLY the required JSON object."
		}
		result, err := architect.Run(ctx, agent.Request{Role: roles.Architect, TaskID: taskID, Workspace: workspace, Prompt: p, Graph: e.graphFor(taskID)}, e.emit)
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
			routing, scoped, err := e.routePlan(ctx, sc, start, taskID, task, d)
			if err != nil {
				return architectureDecision{}, err
			}
			// How expensive this change is decides how adversarially it is
			// judged later, and whether it is routine at all. Recording both
			// here binds them to the moment the plan was authorised, rather
			// than to a preflight taken again further down the loop.
			e.setRouting(taskID, roles.PolicyFor(routing.Blast, routing.Gate), scoped, d.Claims, d.Files)
			e.applyPremiseResolutions(taskID, d.PremiseResolutions)
			var receipt *premiseReceipt
			if routing.ClosesGap() {
				receipt = e.premiseReceiptFor(taskID, routing, routing.ClaimGap)
			}
			switch {
			case routing.Route == RouteCannotEstablish:
				return architectureDecision{}, fmt.Errorf("cannot establish authority for this plan: %s", routing.Condition)
			case routing.ClosesGap() && e.spendClosure(taskID, receipt.ID):
				// Bounded epistemic work, not an owner for the decision.
				// Nothing is granted here: the round establishes what is
				// knowable and the router runs again over what the graph then
				// holds. If the gap did not close, the budget is spent and the
				// next pass falls through to the human branch below with the
				// condition intact.
				e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status,
					"bounded knowledge gap; closing it before governance runs again: "+routing.Condition, map[string]any{"gap": receipt.ID, "gap_identity": routing.Gap}))
				if err := newRound("a bounded knowledge gap"); err != nil {
					return architectureDecision{}, err
				}
				prompt = gapClosurePrompt(prompt, d, routing.Condition+"\n\n"+premiseReceiptNote(receipt.ID))
				attempt = 0
				continue
			case routing.ClosesGap():
				// The budget is spent and the router still reports the same
				// gap. Report it as unclosed rather than looping: what the
				// human is being asked now is not the technical question, it is
				// whether to proceed with the gap still open.
				e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status,
					"the knowledge gap did not close; escalating with it open: "+routing.Condition, nil))
				e.recordClosureQuestion(taskID, routing.Condition, d, start, architect.Label, rounds)
				routing.Route = RouteHuman
				routing.Condition = "a bounded knowledge gap was not closed by investigation: " + routing.Condition
				fallthrough
			case routing.RequiresHuman():
				// Authorizing does not change the graph, so the router will
				// reach this same condition on the next plan. Ask once per
				// condition per task and then honour the answer, or the human
				// is interrogated in a loop and the run never starts.
				if authorized, asked := e.applyAnsweredCondition(taskID, routing.Condition, d.Files...); asked {
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
				if err := newRound("a human authority answer"); err != nil {
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
			routing, scoped, err := e.routePlan(ctx, sc, start, taskID, task, d)
			if err != nil {
				return architectureDecision{}, err
			}
			e.setRouting(taskID, roles.PolicyFor(routing.Blast, routing.Gate), scoped, d.Claims, d.Files)
			e.applyPremiseResolutions(taskID, d.PremiseResolutions)
			if routing.Granted() {
				e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status,
					"architect asked to escalate; Sensei certifies this region, so it is resolved architecturally", nil))
				if err := newRound("an escalation into a region Sensei certifies"); err != nil {
					return architectureDecision{}, err
				}
				prompt = certifiedResolutionPrompt(prompt, d)
				attempt = 0
				continue
			}
			if routing.Route == RouteCannotEstablish {
				return architectureDecision{}, fmt.Errorf("cannot establish authority for this question: %s", routing.Condition)
			}
			if routing.ClosesGap() {
				receipt := e.premiseReceiptFor(taskID, routing, routing.ClaimGap)
				if e.spendClosure(taskID, receipt.ID) {
					e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status,
						"the architect asked to escalate a bounded knowledge gap; closing it instead: "+routing.Condition, map[string]any{"gap": receipt.ID, "gap_identity": routing.Gap}))
					if err := newRound("a bounded knowledge gap"); err != nil {
						return architectureDecision{}, err
					}
					prompt = gapClosurePrompt(prompt, d, routing.Condition+"\n\n"+premiseReceiptNote(receipt.ID))
					attempt = 0
					continue
				}
				e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status,
					"the knowledge gap did not close; escalating with it open: "+routing.Condition, nil))
				e.recordClosureQuestion(taskID, routing.Condition, d, start, architect.Label, rounds)
				routing.Condition = "a bounded knowledge gap was not closed by investigation: " + routing.Condition
			}
			if authorized, asked := e.applyAnsweredCondition(taskID, routing.Condition, d.Files...); asked {
				if !authorized {
					return architectureDecision{}, fmt.Errorf(
						"the human declined this and the architect returned to it: %s", routing.Condition)
				}
				if err := newRound("an answer the human had already given"); err != nil {
					return architectureDecision{}, err
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
			if err := newRound("a human authority answer"); err != nil {
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

// resolveReview runs one independent review of an exact candidate revision.
//
// Three things make it a review rather than a second opinion. The reviewer is
// not the provider that wrote the candidate, and cannot be. Its provider session
// inherits nothing, so it has not read the case for the work before judging it.
// And its verdict is bound to the revision it actually read, so a later edit
// invalidates it instead of silently carrying it forward.
//
// A provider that cannot produce a bounded verdict costs a fallback, not a
// person's attention: being out of quota is not an architectural finding.
func (e *Engine) resolveReview(ctx context.Context, taskID string, assignment roles.Assignment, packet roles.IndependentReviewPacket, implementer string) (roles.ReviewVerdict, error) {
	binding := packet.Provenance.Binding()
	var lastErr error
	for current, ok := assignment, true; ok; current, ok = current.Fallback(current.Provider) {
		cfg, found := e.reviewAgent(current.Provider)
		if !found {
			lastErr = fmt.Errorf("no configured reviewer named %q", current.Provider)
			continue
		}
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.RoleAssigned,
			fmt.Sprintf("%s takes the reviewer role in a session that inherits nothing", config.DisplayName(cfg.Name)),
			map[string]any{
				"role": roles.Reviewer, "provider": cfg.Name, "session": roles.Fresh,
				"excluded": implementer, "candidate": packet.Provenance.CandidateDigest,
			}))
		e.emit(event.New(e.SessionID, taskID, event.SourceReviewer, event.ReviewStarted,
			"independent review of candidate "+shortDigest(packet.Provenance.CandidateDigest), nil))

		verdict, err := e.askReviewer(ctx, taskID, cfg, packet, binding, implementer)
		if err == nil {
			e.reportReview(taskID, verdict)
			return verdict, nil
		}
		lastErr = err
		if errors.Is(err, errReviewRefused) {
			// The verdict was structurally inadmissible -- self-review, or a
			// review of another revision. Another provider would not fix that,
			// and retrying would only produce it again.
			return roles.ReviewVerdict{}, err
		}
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status,
			config.DisplayName(cfg.Name)+" could not produce a bounded review; trying the next independent reviewer",
			map[string]string{"error": err.Error()}))
	}
	return roles.ReviewVerdict{}, fmt.Errorf("no independent reviewer produced a bounded decision: %w", lastErr)
}

// errReviewRefused marks a verdict the workflow will not accept from any
// provider, as opposed to one this provider simply failed to produce.
var errReviewRefused = errors.New("review refused")

func (e *Engine) askReviewer(ctx context.Context, taskID string, cfg config.Agent, packet roles.IndependentReviewPacket, binding roles.Binding, implementer string) (roles.ReviewVerdict, error) {
	reviewer := agent.CLI{Name: cfg.Name, Label: config.DisplayName(cfg.Name), Command: cfg.Command, Args: cfg.Args, NoGraph: !cfg.ConsumesGraph(), Source: event.SourceReviewer, SessionID: e.SessionID, UnsetEnv: provider.SessionOnlyEnv}
	prompt := reviewPrompt(packet)
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		p := prompt
		if attempt > 1 {
			p += "\n\nReturn ONLY the required JSON object."
		}
		result, err := reviewer.Run(ctx, agent.Request{
			Role: roles.Reviewer, TaskID: taskID, Workspace: e.Repo.Root, Prompt: p,
			Session: roles.Fresh, Graph: e.graphFor(taskID),
		}, e.emit)
		if err != nil {
			return roles.ReviewVerdict{}, err
		}
		var d reviewDecision
		if err := decodeModelJSON(result.Text, &d); err != nil {
			lastErr = err
			continue
		}
		verdict := roles.ReviewVerdict{
			Provenance: roles.Provenance{
				TaskID: taskID, Role: roles.Reviewer, Provider: cfg.Name,
				SessionID: e.SessionID, SessionMode: result.Session,
				BaseSHA: binding.BaseSHA, CandidateDigest: binding.CandidateDigest,
				GraphBuildCommit: packet.Provenance.GraphBuildCommit,
				At:               time.Now().UTC(),
			},
			Decision:     roles.Decision(strings.ToLower(strings.TrimSpace(d.Decision))),
			Summary:      strings.TrimSpace(d.Summary),
			Instructions: d.Instructions,
			Findings:     numberFindings(d.Findings),
		}
		if err := verdict.Validate(binding, implementer); err != nil {
			if reviewIsInadmissible(verdict, binding, implementer) {
				return roles.ReviewVerdict{}, fmt.Errorf("%w: %v", errReviewRefused, err)
			}
			lastErr = err
			continue
		}
		return verdict, nil
	}
	return roles.ReviewVerdict{}, lastErr
}

// reviewIsInadmissible separates a verdict no provider may give from one this
// provider merely got wrong. The first must stop the review; the second is
// worth asking again for.
func reviewIsInadmissible(v roles.ReviewVerdict, b roles.Binding, implementer string) bool {
	if b.Mismatch(v.Provenance) != "" {
		return true
	}
	if implementer != "" && strings.EqualFold(implementer, v.Provenance.Provider) {
		return true
	}
	return !v.Provenance.Independent()
}

// reportReview writes the review into the record as findings rather than as one
// line. A review compressed to a sentence loses the only part a worker can act
// on, and the compression is invisible afterwards.
func (e *Engine) reportReview(taskID string, v roles.ReviewVerdict) {
	for _, f := range v.Findings {
		e.emit(event.New(e.SessionID, taskID, event.SourceReviewer, event.ReviewFinding, f.Line(), f))
	}
	e.emit(event.New(e.SessionID, taskID, event.SourceReviewer, event.ReviewCompleted,
		strings.ToUpper(string(v.Decision))+": "+v.Summary+"  ("+v.Provenance.Describe()+")", v))
}

// numberFindings gives every finding an id, because a finding referred to as
// "the second one" cannot be tracked across a revision.
func numberFindings(findings []roles.Finding) []roles.Finding {
	out := make([]roles.Finding, 0, len(findings))
	for i, f := range findings {
		if strings.TrimSpace(f.ID) == "" {
			f.ID = fmt.Sprintf("f%d", i+1)
		}
		if strings.TrimSpace(string(f.Severity)) == "" {
			// An unrated finding is reported as major rather than dropped or
			// promoted: dropping it loses a real objection, and promoting it to
			// blocking would let a missing field refuse a candidate.
			f.Severity = roles.Major
		}
		out = append(out, f)
	}
	return out
}

func shortDigest(d string) string {
	d = strings.TrimSpace(d)
	if d == "" {
		return "(unbound)"
	}
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

// routePlan asks Sensei whether the region this plan intends to touch is one it
// can certify, and routes authority on the answer.
//
// This is the first preflight in the workflow that can name files: the start
// gate ran before a plan existed. The architect's own decision is not consulted
// here and is not a parameter.
func (e *Engine) routePlan(ctx context.Context, sc *sensei.Client, start certifiedStart, taskID, task string, d architectureDecision) (Routing, sensei.PreflightDecision, error) {
	args := map[string]any{"task": task, "files": d.Files, "mode": "compact"}
	if domain := start.Domain(); domain != "" {
		args["domain"] = domain
	}
	result, err := sc.CallTool("awareness_preflight", args)
	if err != nil {
		return Routing{}, sensei.PreflightDecision{}, fmt.Errorf("Sensei scoped preflight: %w", err)
	}
	scoped, err := sensei.DecodePreflight(result)
	if err != nil {
		return Routing{}, sensei.PreflightDecision{}, err
	}
	// The scoped decision is returned as well as the route. The router reduces
	// it to one question -- who owns this plan -- and the Level-1 classifier
	// asks a different one of the same evidence. Re-querying instead would ask
	// a graph that may have moved between the two questions, and the answers
	// would then describe different moments while appearing to describe one.
	// The action the authority decision actually covers.
	//
	// Stage is set HERE, from what the governed workflow does, and never from
	// anything a provider returned: the architect stage edits inside an
	// isolated candidate worktree whose diff is audited before it can leave.
	// The plan's own steps and consequences travel with it as claims, which the
	// assessment may use to escalate and never to clear.
	stage := StageCandidateEdit
	if e.observes(taskID) {
		stage = StageObserve
	}
	// Machine-derived coverage, revalidated in THIS world over THESE files.
	//
	// This was absent, and the absence was silent. The router's only coverage
	// input was the graph's own, so a bounded knowledge gap could be reported,
	// a closure round could run, the architect could investigate -- and no
	// channel existed by which any of that reached the decision. The gap could
	// not close, so it escalated to a human every time. Measured: three of five
	// governed refusals in the proof-v6 campaign were unclosed coverage gaps
	// whose closure round ran and failed for exactly this reason.
	//
	// Nothing is granted by supplying it. derivationClosesGap decides whether a
	// derivation RESOLVES this gap, not merely whether it is true over the same
	// files, and a derivation that does not resolve it leaves the region
	// uncovered and the refusal intact. The approval gate is checked before
	// coverage and is unaffected.
	//
	// Failure is silence: a missing recipe file, a sensei binary without
	// `derive`, or a derivation that refuses each leave the list empty, which is
	// the direction this must fail in.
	action := Action{
		Stage:                stage,
		Files:                d.Files,
		DeclaredSteps:        d.Steps,
		DeclaredConsequences: d.Consequences,
		DerivedCoverage:      e.derivedCoverage(ctx, taskID, d.Files, d.ProspectiveSurfaces),
	}
	action.OperationalAuthority = operationalFiles(e.testEditGrants(taskID))
	routing := routeAuthorityForAction(scoped, d.Claims, action)
	// The gap's identity is completed with the world it was met in. The
	// router does not know the pinned base; the budget must, or the same gap
	// at two bases would share one round.
	if routing.Gap.Identified() {
		routing.Gap.World = strings.TrimSpace(e.governedBase(taskID))
	}

	// Observational only. Nothing below reads it and no decision depends on it.
	//
	// The routing decision above is already made. This records WHAT REACHED the
	// router, because the event stream could not answer that question: a reader
	// could see the condition a gap produced and not whether any derivation was
	// available to close it. Distinguishing "the channel carried nothing" from
	// "it carried evidence that did not resolve the gap" is the difference
	// between two unrelated repairs.
	//
	// Emitted after the routing so it cannot influence it, and carrying counts
	// and identities rather than a verdict.
	if len(action.DerivedCoverage) != 0 || routing.ClosesGap() {
		files := make([]string, 0, len(action.DerivedCoverage))
		for _, c := range action.DerivedCoverage {
			files = append(files, c.File+" ["+string(c.Requirement)+"]")
		}
		summary := fmt.Sprintf("derived coverage: %d anchor(s) over %d planned file(s); route %s",
			len(action.DerivedCoverage), len(d.Files), routing.Route)
		if len(files) != 0 {
			summary += "\n  " + strings.Join(files, "\n  ")
		}
		if op := action.OperationalAuthority; len(op) != 0 {
			// Stated as its own kind, beside coverage and never summed with it.
			summary += fmt.Sprintf("\n  operational authority (existing-test edit): %d file(s): %s", len(op), strings.Join(op, ", "))
		}
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status, summary,
			map[string]any{
				"derived_coverage_anchors": len(action.DerivedCoverage),
				"planned_files":            len(d.Files),
				"route":                    string(routing.Route),
				"closes_gap":               routing.ClosesGap(),
				"anchors":                  files,
				"operational_authority":    action.OperationalAuthority,
			}))
	}

	// What sensei-code can now SAY about this plan: which part came from the
	// requested objective, which are technical claims still needing evidence,
	// and which consequence decision remains authority-sensitive.
	//
	// Stated, not decided. The route above is already final; this reads the
	// same inputs and separates them, so a reader can see that the objective
	// established none of the technical premises and that the consequence
	// assessment did not consult who asked.
	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status,
		StateAuthority(e.objective(taskID), d.Claims, AssessConsequences(action), routing.Route, d).Render(), nil))

	return routing, scoped, nil
}

// derivedRecipesPath is where the durable questions live.
const derivedRecipesPath = "docs/awareness/derived_recipes.json"

// derivedReceiptsPath is the append-only log of investigator runs.
//
// Separate from the recipes, and append-only, because V2 §6.3 requires a
// receipt per inference RUN. A run that proposed a duplicate or proposed
// nothing still ran, and those are the recurrence and decline signals; storing
// receipts inside recipes would discard exactly the ones that carry them.
const derivedReceiptsPath = "docs/awareness/derived_receipts.jsonl"

// senseiBinary is the CLI that performs derivations.
//
// Separate from Config.Sensei, which names the MCP server. SENSEI_BIN exists so
// a checkout can point at a build carrying `derive` without reinstalling.
func senseiBinary() string {
	if b := strings.TrimSpace(os.Getenv("SENSEI_BIN")); b != "" {
		return b
	}
	return "sensei"
}

// derivedCoverage revalidates the committed recipes against the world being
// assessed and returns the planned files a derivation established THERE, each
// with the architectural question that derivation can answer.
//
// Reached by the governed run: routePlan supplies it on every routing decision.
//
// It was NOT, and the comment here said so, correctly, for as long as that was
// true. The wiring was deliberately deferred because connecting it WIDENS
// autonomy, and the evidence that justified connecting it arrived from the
// proof-v6 campaign: three of five governed refusals were coverage gaps whose
// closure round ran and could not close, because no channel carried a
// derivation to the router. TestTheGovernedRunSuppliesDerivedCoverage now pins
// the channel, and five sibling tests pin that insufficient evidence still
// refuses.
//
// The only thing reaching the router is a list of files a derivation just
// succeeded over. Recipes do not reach it, and neither does any earlier
// success: a fact that derived yesterday, or at another commit, has no standing
// here. A recipe costs one derivation and buys nothing on its own.
//
// Failure is silence rather than coverage. A missing recipe file, a sensei
// binary without `derive`, a derivation that refuses — each leaves the region
// uncovered and the gap intact, which is the direction this must fail in.
// coverageComputation is what one look at the pinned world establishes:
// coverage, prospective grants, existing-test edit grants, and the reasons
// a test was not granted. It is a VALUE. Computing it records nothing and
// emits nothing, so a resume can re-establish what a run's record claims
// without writing a record of its own -- a second resume that read such a
// write would find an authority the original run never held (sensei-code#101
// review).
type coverageComputation struct {
	world       string
	coverage    []CoverageAnchor
	prospective []prospectiveGrant
	edits       []testEditGrant
	reasons     []string
}

// coverageAtWorld computes coverage and grants for a plan at the candidate's
// pinned base, side-effect free. See coverageComputation.
func (e *Engine) coverageAtWorld(ctx context.Context, taskID string, planned []string, declarations []ProspectiveSurface) (coverageComputation, bool) {
	if len(planned) == 0 {
		return coverageComputation{}, false
	}
	recipes, err := derived.LoadRecipes(filepath.Join(e.Repo.Root, derivedRecipesPath))
	if err != nil || len(recipes) == 0 {
		return coverageComputation{}, false
	}
	// The world is the candidate's pinned base, not the canonical HEAD. The
	// worktree is cut from that base; facts read from a HEAD that advanced
	// between establishing the identity and finishing routing would authorize
	// a creation against a package and import set the candidate does not
	// contain. Only before an identity exists (the observation lane) does
	// HEAD stand in, and no grant can be issued there.
	world := strings.TrimSpace(e.governedBase(taskID))
	if world == "" {
		head, err := e.Repo.Head(ctx)
		if err != nil {
			return coverageComputation{}, false
		}
		world = strings.TrimSpace(head)
		if world == "" {
			return coverageComputation{}, false
		}
		declarations = nil
	}
	// The future-only rule, applied before any derivation is spent.
	//
	// A closure round that writes a question must not be covered by it. If it
	// were, a run that could not establish its own authority would have
	// established it by writing something down -- the self-approval this design
	// exists to refuse. Encounter 1 writes; encounter 2 benefits.
	recipes = derived.ExcludingTask(recipes, taskID)
	anchors, _ := derived.AnchorsFor(ctx, derived.CLI{Bin: senseiBinary()}, e.Repo.Root, world, recipes)
	grants, out := coverPlannedAtWorld(ctx, world, planned, declarations, anchors, gitShowAt(e.Repo.Root))
	edits, reasons := testEditGrants(ctx, world, planned, out, gitShowAt(e.Repo.Root))
	return coverageComputation{world: world, coverage: out, prospective: grants, edits: edits, reasons: reasons}, true
}

// derivedCoverage is the ROUTING path: it computes coverage at the world and
// then records what routing will act on -- the prospective and test-edit
// grants on the engine and in the session -- so a resume can re-establish
// them against the record. Only routing writes; a resume computes (see
// coverageAtWorld) and compares.
func (e *Engine) derivedCoverage(ctx context.Context, taskID string, planned []string, declarations []ProspectiveSurface) []CoverageAnchor {
	c, ok := e.coverageAtWorld(ctx, taskID, planned, declarations)
	if !ok {
		return nil
	}
	e.setProspectiveGrants(taskID, c.prospective)
	e.setTestEditGrants(taskID, c.edits)
	if len(c.edits) != 0 {
		names := make([]string, 0, len(c.edits))
		for _, g := range c.edits {
			names = append(names, g.Path+" beside "+g.Covering)
		}
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.TestEditGranted,
			"existing-test edit authority recorded for "+strings.Join(names, ", ")+" (operational, not coverage)",
			testEditRecord{World: c.world, Grants: c.edits}))
	}
	for _, r := range c.reasons {
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status, "no test-edit authority: "+r, nil))
	}
	if len(c.prospective) != 0 {
		names := make([]string, 0, len(c.prospective))
		for _, g := range c.prospective {
			names = append(names, g.Anchor.File+" by "+g.Covering)
		}
		// The authorization is recorded verbatim, bound to the world it was
		// read at, so a task resumed after a restart inspects its created
		// files against these facts and not against a fresh read.
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.ProspectiveGranted,
			"prospective authority recorded for "+strings.Join(names, ", "),
			prospectiveRecord{World: c.world, Grants: c.prospective}))
	}
	return c.coverage
}

// restoreProspectiveGrants re-establishes, from the session record, the
// prospective authorization a resumed task's inspection must check against.
//
// A task that declared surfaces but has no intact record for them is not
// resumed: routing is not re-run on resume, so nothing else could establish
// the facts, and inspecting against an empty set would either refute a valid
// creation or accept one whose authorization was never recorded. A record from
// another world is refused for the same reason.
func (e *Engine) restoreProspectiveGrants(task session.Interrupted, declared []ProspectiveSurface, world string) error {
	if len(declared) == 0 {
		return nil
	}
	var rec prospectiveRecord
	if len(task.ProspectiveRecord) == 0 || json.Unmarshal(task.ProspectiveRecord, &rec) != nil {
		return fmt.Errorf("cannot resume %s: it declares prospective surfaces but the session record holds no prospective authorization for them", task.TaskID)
	}
	if strings.TrimSpace(rec.World) == "" || rec.World != strings.TrimSpace(world) {
		return fmt.Errorf("cannot resume %s: the recorded prospective authorization was read at world %s, not the candidate's pinned base %s", task.TaskID, shortWorldID(rec.World), shortWorldID(world))
	}
	// A record at the right world is not yet the receipt. The grants must be
	// exactly the authorization for these declarations, or nothing is
	// restored and the resume refuses before implementation.
	if err := matchGrantsToDeclarations(declared, rec.Grants); err != nil {
		return fmt.Errorf("cannot resume %s: %w", task.TaskID, err)
	}
	e.setProspectiveGrants(task.TaskID, rec.Grants)
	return nil
}

// coverPlannedAtWorld turns revalidated anchors into coverage over the planned
// files, partitioned FIRST by whether each file exists at the pinned world.
//
// Ordinary derived coverage is restricted to files proven present there. A
// derived.Anchor covers by comparing world and subject paths; it does not
// establish that a subject exists, so an anchor naming a not-yet-created path
// would otherwise give a new file ordinary coverage and bypass the whole
// prospective predicate (sensei#312 cycle 2).
//
// Existence is tri-state. Present files take ordinary coverage; files the
// world's tree provably lacks (errNotAtWorld) are the only ones that reach
// prospectiveAnchors; a file whose read failed for any other reason is
// UNREADABLE and takes neither: an unanswered read is not absence, and a
// transient failure to read F must not become authority to create it (cycle
// 3). Undeclared missing files stay uncovered.
func coverPlannedAtWorld(ctx context.Context, world string, planned []string, declarations []ProspectiveSurface, anchors []derived.Anchor, read worldReader) ([]prospectiveGrant, []CoverageAnchor) {
	if read == nil {
		return nil, nil
	}
	var existing, missing []string
	for _, f := range planned {
		_, err := read(ctx, world, f)
		switch {
		case err == nil:
			existing = append(existing, f)
		case confirmedMissing(err):
			missing = append(missing, f)
		default:
			// Unreadable: neither present nor confirmed missing. Uncovered.
		}
	}
	covered, _ := derived.CoveredFiles(anchors, world, existing)

	// Each covered file carries what the derivation that covered it is able to
	// ANSWER, computed here from the anchor's family. The family mapping lives
	// in relevance.go, in this consumer: a recipe is data anyone may commit, so
	// a family that declared its own relevance would be self-labelling.
	var out []CoverageAnchor
	for _, f := range covered {
		for _, a := range anchors {
			if !a.Covers(world, f) {
				continue
			}
			out = append(out, CoverageAnchor{
				File:        f,
				Requirement: requirementOfFamily(a.Kind()),
				Describe:    a.Describe(),
			})
		}
	}

	// Prospective authority (sensei#312): a planned file ABSENT at world can be
	// covered only by established facts about a covering surface in its
	// directory plus the plan's declaration. The covering surfaces are every
	// subject file a derivation established here, planned or not, read from
	// the pinned world and never the working tree (a subject that cannot be
	// read there is no surface). Undeclared absent files stay uncovered.
	var surfaces []CoverageAnchor
	for _, a := range anchors {
		for _, f := range a.Files() {
			if !a.Covers(world, f) {
				continue
			}
			if _, err := read(ctx, world, f); err != nil {
				continue
			}
			surfaces = append(surfaces, CoverageAnchor{File: f, Requirement: requirementOfFamily(a.Kind()), Describe: a.Describe()})
		}
	}
	grants := prospectiveAnchors(ctx, world, missing, declarations, surfaces, read)
	for _, g := range grants {
		if len(g.Anchors) == 0 {
			out = append(out, g.Anchor)
			continue
		}
		out = append(out, g.Anchors...)
	}
	return grants, out
}

// setProspectiveGrants records what prospective authority the router read for
// a task; prospectiveGrants returns it for the post-creation inspection.
func (e *Engine) setProspectiveGrants(taskID string, grants []prospectiveGrant) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.prospective == nil {
		e.prospective = make(map[string][]prospectiveGrant)
	}
	e.prospective[taskID] = grants
}

func (e *Engine) prospectiveGrants(taskID string) []prospectiveGrant {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.prospective[taskID]
}

func (e *Engine) awaitHuman(ctx context.Context, sc *sensei.Client, start certifiedStart, taskID string, d architectureDecision, condition string) (string, error) {
	// Human authority is deliberately presented as a tiny numbered decision
	// surface. Model-supplied option IDs and labels are not authority: compose
	// normalizes the IDs and assigns every outcome itself.
	options := composeAuthorityOptions(d.Options)
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
	return e.awaitChoice(ctx, sc, taskID, condition, start.Domain(), e.governedBase(taskID), decision, options, d.Files...)
}

// awaitChoice presents a numbered decision to the human and blocks until they
// answer. It is the single place a task waits on a person, so every gate --
// architectural authority and plan approval alike -- looks the same in the UI.
// governedBase is the commit this repository's candidate was pinned to.
//
// It is deliberately not the graph's SourceRepoCommit, which an earlier version
// of this call passed. That field is the commit identity of the rule snapshot,
// and on this installation it belongs to the services repository -- gate.go
// says so in as many words, having already been bitten by comparing the two.
// Passing it here labelled another repository's commit as this resolution's
// base, and a governance receipt that cites a commit the repository does not
// contain is evidence about nothing.
//
// An unestablished identity yields "" rather than a substitute. A resolution
// with no stated base is honest; one with somebody else's base is not.
func (e *Engine) governedBase(taskID string) string {
	identity, ok, err := candidate.Load(e.Repo.Root, taskID)
	if err != nil || !ok {
		return ""
	}
	return identity.BaseSHA
}

func (e *Engine) awaitChoice(ctx context.Context, sc *sensei.Client, taskID, condition, domain, baseSHA string, decision authority.Decision, options []authority.Option, scope ...string) (string, error) {
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
		if choice == deferToken {
			// Deliberately not an AuthorityResolved event: nothing was
			// resolved. The question is recorded whole, and the run ends
			// without touching the candidate, calling a provider, or asking
			// Sensei anything further.
			e.emit(event.New(e.SessionID, taskID, event.SourceUser, event.WorkflowAwaitingAuthority,
				"authority decision deferred; the question stands", DeferredAuthority{
					Condition: condition, Domain: domain, BaseSHA: baseSHA, Decision: decision,
				}))
			return "", errAuthorityDeferred
		}
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
						Scope:   scope,
						Outcome: option.Outcome,
					}
					if option.Outcome == authority.Stop {
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

				if option.Outcome == authority.Stop {
					return "", errors.New("task stopped by human authority")
				}
				return option.ID + ": " + option.Label, nil
			}
		}
		return "", fmt.Errorf("unknown human authority option %q", choice)
	}
}

// composeAuthorityOptions builds the human's decision surface.
//
// The architect may propose alternatives; it may not decide what choosing one
// means. Every option it supplies is stamped as an authorization of that
// alternative by this repository, and the two refusals are always our own words
// and always present. A model can therefore widen what a human may say yes to,
// and can never remove, reword, or crowd out the way to say no.
//
// This exists as its own function because the property is worth testing
// directly: the previous arrangement decided the meaning of an answer by
// matching substrings in labels the architect wrote.
func composeAuthorityOptions(proposed []authority.Option) []authority.Option {
	const maxProposed = 2
	options := make([]authority.Option, 0, maxProposed+2)
	for _, option := range proposed {
		if len(options) == maxProposed {
			break
		}
		if strings.TrimSpace(option.Label) == "" {
			continue
		}
		option.Outcome = authority.Authorize
		options = append(options, option)
	}
	if len(options) == 0 {
		options = append(options, authority.Option{
			Label:   "Authorize the architectural change described above",
			Outcome: authority.Authorize,
		})
	}
	options = append(options,
		authority.Option{
			Label:   "Require another design (this change may still be right; this plan is not)",
			Outcome: authority.Revise,
		},
		authority.Option{Label: "Stop this task", Outcome: authority.Stop},
	)
	for i := range options {
		options[i].ID = fmt.Sprint(i + 1)
	}
	return options
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

func architecturePrompt(repoRoot, domain, architectName, task, conversation, workspaceStatus, preflight,
	retrievedEvidence, repositoryEvidence, standingContext, recordedHistory string) string {
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
  "mode": "modify" | "inspect",
  "related_invariants": ["existing Sensei invariant id this work is governed by"],
  "prospective_surfaces": [{"path":"pkg/x_test.go","package":"x","role":"go-regression-test","dependencies":["testing"]}],
  "human_question": "only when escalating",
  "recommendation": "option id only when escalating",
  "options": [{"id":"1","label":"...","description":"..."}],
  "claims": [{"statement":"the factual premise","about":"path or component it concerns","source":"graph|repository|inference","gap":"only the receipt id of an unsettled premise this claim continues"}],
  "premise_resolutions": [{"gap":"receipt id you were asked to answer","outcome":"established|refuted|unresolved","evidence":"..."}]
}
Declare every file the plan CREATES under "prospective_surfaces" (the only role is
go-regression-test: a *_test.go beside a covered file, importing nothing beyond that file's
imports and "testing"); an undeclared new file stays uncovered.
MODE IS REQUIRED WHENEVER YOU PROCEED.
  modify   - the plan edits this repository. A worker is expected to produce a diff.
  inspect  - the plan reads and reports and changes nothing: an audit, an
             investigation, a review. A worker is expected to produce findings
             and no diff, and a diff would be out of scope.
Choose inspect whenever the human asked to be told something rather than to have
something changed. Listing files under "files" does not make a plan modify: an
audit reads many files and changes none.

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
This is the UNSCOPED preflight. It is thin by construction: there are no files
to scope it to until you have written a plan. Read it as "what the graph can say
before knowing what you will touch", not as a finding that this work is
ungoverned. The sections below are what the graph does hold about the subject.
%s

WHAT SENSEI HOLDS ABOUT THIS SUBJECT:
Retrieved from the graph by the task's own terms. These are the invariants,
failure modes, forbidden fixes and tests that govern the region you are planning
in. Plan inside them, and name any you believe the work must cross.
%s

REPOSITORY EVIDENCE:
Read from this checkout, for the files the retrieval above pointed at. It is what
the code currently does, not what anyone remembers it doing.
%s

WHAT THIS REPOSITORY HAS ALREADY DECIDED AND FOUND:
Read from committed records, not recalled. Authority decisions bind: a condition
this repository has answered has been answered, and re-opening one is itself an
architectural act. Audit findings are what was true when the audit ran.
%s

WHERE THIS PROJECT STANDS:
Folded from this session's durable record: work in flight, candidates left
standing, decisions already taken. It is context for what you are joining, and
it authorises nothing on its own.
%s`, architectName, repoRoot, domain, conversationOrNone(conversation), task, workspaceStatus, preflight,
		orNone(retrievedEvidence, "(the graph returned nothing for this subject)"),
		orNone(repositoryEvidence, "(no repository evidence was gathered for this turn)"),
		orNone(recordedHistory, "(this repository has recorded no decisions or audits)"),
		orNone(standingContext, "(this session has no standing work)"))
}

// orNone keeps a section from reading as truncation when it is genuinely empty.
// A blank heading looks like a prompt that lost its content; a stated absence is
// an answer.
func orNone(value, absent string) string {
	if strings.TrimSpace(value) == "" {
		return absent
	}
	return value
}

func implementationPrompt(tc taskContext, plan, feedback string, cycle int, guidance []string, grants string) string {
	extra := ""
	if strings.TrimSpace(grants) != "" {
		extra += `

PROSPECTIVE CREATE GRANTS -- the authority you already operate under for files this plan creates.
This section does not widen your scope; it states the exact shape the run authorized so you can
build inside it instead of discovering it afterwards. Each created file is inspected against its
grant before any review, and a file whose package or imports fall outside the grant ends the run
with no retry. If the plan cannot be implemented honestly inside these imports, do NOT add one:
leave that file uncreated and say in your output exactly which import the plan needs and why,
so the architect can decide. Widening the envelope is the architect's decision, not yours.
` + grants
	}
	if strings.TrimSpace(feedback) != "" {
		extra += "\n\nREVIEW FEEDBACK TO RECONCILE:\n" + feedback
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
	// The worker is told which kind of work it was given. Without this an
	// inspection worker read "you may edit" and an audit that edited was caught
	// only afterwards, by refusing a candidate the worker had been invited to
	// produce.
	role := `Implement only the architect's bounded plan. You may inspect, edit, build, and test inside this candidate worktree.`
	if tc.Mode == ModeInspect {
		role = `THIS PLAN IS READ-ONLY. Do not edit, create, or delete any file in this worktree, and do not commit.
Your deliverable is your final message: the findings the plan asked for. Nothing else you produce is kept.
State the evidence behind each finding -- the file and line, the command you ran and its output, or what Sensei returned -- and mark anything you could not establish as unverified rather than asserting it.

A SEARCH THAT FOUND NOTHING HAS NOT PROVEN ANYTHING ABSENT.
This is the single mistake inspections here make most. "I grepped for X and got
no hits" establishes that your search found nothing. It does not establish that
X does not exist: the thing may be spelled differently, reached through an
interface, constructed by reflection, generated at build time, or live somewhere
your search did not go. Sensei draws exactly this line and so must you --
EmptyProven means "I looked here and there was nothing"; Absent means "nothing
exists". They are different claims and only the first is yours to make.
So write "no consumer appears in <these searches over these paths>", not "no
consumer exists -- proven". Name the searches and their bounds, so a reader can
run them. A negative you cannot hand someone to re-run is not a finding.

CHECK YOUR FINDINGS AGAINST EACH OTHER BEFORE YOU SEND THEM.
Two findings that contradict each other mean at least one is wrong, and the
reader cannot tell which. If you cannot reconcile them, say which one you are
less sure of and why, rather than shipping both as established.

End with what this inspection did NOT cover, and say plainly if the plan asked for something you could not do. A finding without evidence is an opinion, and an inspection that hides its limits overstates itself.
An independent reviewer reads these findings and decides whether they are supported, so unsupported claims come back to you rather than standing.`
	}
	return fmt.Sprintf(`You are a bounded implementation worker operating in an isolated Git worktree.
%s Work autonomously: do not ask the user for routine permissions.
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

CYCLE: %d%s`, role, conversationOrNone(tc.Conversation), tc.intent(), tc.WorkspaceStatus, tc.Preflight, tc.Task, plan, cycle, extra)
}

func reviewPrompt(p roles.IndependentReviewPacket) string {
	if p.Inspection() {
		return inspectionReviewPrompt(p)
	}
	return fmt.Sprintf(`You are the architectural reviewer for a Sensei-governed candidate. Do not edit files.
Decide whether the exact candidate satisfies the architectural plan and the supplied Sensei evidence. Passing tests alone is not architectural proof.
Return ESCALATE only when a genuine architectural-authority question exists; ordinary defects are REVISE.

You are reviewing independently. You did not write this change, you have not
been told why its author believes it is right, and you are not being asked to
agree with anybody. Assume the candidate may be wrong even when the tests are
green, and attack the diff and its proof: stale evidence, authority bypasses,
unbound identity, hidden scope expansion, and PASS states that prove less than
they appear to.

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
{"decision":"accept"|"revise"|"escalate","summary":"...","instructions":"specific repair instructions when revise/escalate",
 "findings":[{"id":"f1","severity":"blocking"|"major"|"minor","claim":"what the candidate or its evidence asserts that you do not accept","reference":"file, component, or piece of evidence","reason":"why","correction":"the repair required","proof_gap":"the proof that is missing, if that is the issue"}]}

Every blocking finding must name something a worker can open. A finding with
nowhere to point cannot be acted on, and a cycle spent on one produces an
identical diff and consumes the budget for nothing. Do not return ACCEPT while
recording a blocking finding.

CANDIDATE REVISION: %s
This verdict is bound to that revision. If the worker changes the candidate,
your verdict no longer applies to it and will not be carried forward.

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

ARCHITECTURAL PLAN%s:
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
%s`, shortDigest(p.Provenance.CandidateDigest), conversationOrNone(p.Conversation), intentOrNone(p.ArchitectIntent),
		p.WorkspaceAuthority, p.Preflight, p.Task, planSourceLabel(p), p.Plan, p.Audit, p.Validation, p.Diff)
}

// planSourceLabel qualifies the plan heading a reviewer reads. An architect's
// plan is the unmarked case, exactly as before; a supplied plan says so, so the
// reviewer does not weigh it as the architect's judgement.
func planSourceLabel(p roles.IndependentReviewPacket) string {
	if p.PlanSource == string(PlanSupplied) {
		return " (supplied with the task, not architect-produced; sha256 " + p.PlanDigest + ")"
	}
	return ""
}

// inspectionReviewPrompt judges findings rather than a change.
//
// The questions are different ones. A change is asked whether it is safe; a set
// of findings is asked whether it is supported -- and the characteristic failure
// is not an unsafe edit but a confident claim with nothing behind it, or an
// inspection that quietly skipped most of what it was asked to cover and
// reported only what it happened to look at.
func inspectionReviewPrompt(p roles.IndependentReviewPacket) string {
	return fmt.Sprintf(`You are the independent reviewer of a read-only inspection carried out under a Sensei-governed plan. Do not edit files.
The worker was asked to change nothing and to report findings. It changed nothing. Your job is to decide whether the findings it reported are SUPPORTED, not whether you would have written them.

You are reviewing independently. You did not produce these findings, you have not
been told why their author believes them, and you are not being asked to agree.
Assume a finding may be wrong, overstated, or unevidenced even when it is
plausible and well written.

Judge the report on:
  - EVIDENCE. Does each finding name something checkable -- a file and line, a
    command and its output, what Sensei returned? A finding that only reasons
    about the code is a hypothesis, and must be labelled one.
  - SCOPE. Did the inspection cover what the plan asked for? Findings about the
    easy part, silently omitting the rest, is the failure this review exists to
    catch. Silence about an area the plan named is not a clean bill of health.
  - LIMITS. Does the report say what it did NOT establish? A report with no
    stated limitations is usually a report that stopped looking.
  - OVERSTATEMENT. Is any finding's severity or confidence higher than its
    evidence carries?

You have Sensei's own MCP tools in this session: awareness_briefing,
awareness_preflight, awareness_impact, awareness_query, awareness_resolve,
awareness_metadata and sensei_workspace_status. Check the report's claims about
governance against what Sensei actually holds, and quote what Sensei said. There
is no diff audit here because there is no diff.

REVISE when findings lack the evidence to stand, when the plan's scope was not
covered, or when limits are missing. ACCEPT means: these findings are supported
and honestly bounded. It does not mean you agree with every judgement in them.
ESCALATE only for a genuine architectural-authority question.

Return ONLY JSON:
{"decision":"accept"|"revise"|"escalate","summary":"...","instructions":"what the inspection must add or establish when revise/escalate",
 "findings":[{"id":"f1","severity":"blocking"|"major"|"minor","claim":"the assertion in the report you do not accept as established","reference":"the finding or section","reason":"why","correction":"the evidence or coverage required","proof_gap":"what is missing"}]}

CONVERSATION WITH THE HUMAN:
%s

ARCHITECT INTENT:
%s

SENSEI WORKSPACE AUTHORITY:
%s

SENSEI PREFLIGHT:
%s

TASK:
%s

THE READ-ONLY PLAN THE WORKER WAS BOUND BY%s:
%s

THE FINDINGS REPORTED:
%s`, conversationOrNone(p.Conversation), p.ArchitectIntent,
		p.WorkspaceAuthority, p.Preflight, p.Task, planSourceLabel(p), p.Plan, p.Report)
}

// intentOrNone keeps the reviewer's packet unambiguous the same way the
// architect's conversation is: a blank section reads as truncation.
func intentOrNone(intent string) string {
	if strings.TrimSpace(intent) == "" {
		return "(the architect recorded no additional rationale)"
	}
	return intent
}

func escalationPrompt(task, plan, audit string, review roles.ReviewVerdict) string {
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

Return ONLY the same architecture JSON contract as before.`, task, plan, audit, review.Summary, review.Instruction())
}

// humanResolutionPrompt continues an architect whose question a person just
// answered.
//
// It carries the scope that person authorised, which it did not used to. The
// architect received the choice and the previous escalation's summary and
// nothing about the plan itself, so it re-planned from the task rather than
// revising the plan that was approved -- and produced a different file list
// every round. Since an answer covers a plan that stays inside what was
// authorised, every drift outward became a fresh question, and the same
// boundary was put to the person again.
//
// Observed twice on 2026-08-22: a seven-file plan was authorised and the next
// plan named nine, three of them never seen, including internal/candidate/
// identity.go. The gate was right to re-ask. The drift was not necessary.
//
// The instruction is deliberately not "you may not add files". An architect
// that discovers it genuinely needs more must be able to say so -- suppressing
// that would trade an honest question for a quiet omission, which is the worse
// failure. What it must not do is wander, and it must name what it adds.
func humanResolutionPrompt(original string, d architectureDecision, choice string) string {
	return fmt.Sprintf(`%s

HUMAN AUTHORITY RESOLUTION:
The human selected %s.
Apply that decision exactly. You now have architectural authority to produce a bounded implementation plan within that human choice.
Return ONLY architecture JSON with decision="proceed" unless the human choice itself exposes a different unresolved human-owned boundary.

THE SCOPE THE HUMAN AUTHORISED:
%s

This is what they saw and agreed to. Revise the plan they approved rather than
writing a new one: keep "files" inside this set. Dropping a file is free — a
narrower plan stays within what was authorised.

Adding one is not. Every file outside this set is work the person has not seen,
so it puts the same boundary back in front of them and they are asked again. Do
not add a file because it might be interesting to read. If the work genuinely
cannot be done inside this scope, add what it needs and say in "summary" which
files you added and why each is necessary — an honest widening they can judge is
right, and wandering is not.

PREVIOUS ESCALATION:
%s`, original, choice, renderScope(d.Files), d.Summary)
}

// renderScope lists the authorised files, or says plainly that none were named.
// A blank list would read as "no constraint" to a model, which is the opposite
// of what an unscoped authorisation means.
func renderScope(files []string) string {
	if len(files) == 0 {
		return "(the approved plan named no files, so there is no authorised set to stay inside; " +
			"name the files your plan touches and expect to be asked again)"
	}
	return "- " + strings.Join(files, "\n- ")
}

// certifiedResolutionPrompt answers an architect that asked to escalate a
// question Sensei can in fact certify.
//
// The reply is deliberately not "you were wrong to ask". The architect gets the
// certification as evidence and is asked to decide on it, because the useful
// behaviour to reinforce is asking when unsure, not staying quiet — what must
// not happen is the asking alone interrupting a human.
// recordClosureQuestion durably records what a failed closure round found worth
// checking.
//
// Called ONLY where a gap could not be closed, so this is not a general-purpose
// recipe generator: no gap, no question. It records a QUESTION and never a rule,
// an invariant, a contract, a decision, a derived fact or an authority grant --
// those still need a human, correctly, until dq.closure_knowledge_admission is
// answered.
//
// Nothing about the CURRENT task changes. The escalation proceeds exactly as it
// would have; the run that writes the question is excluded from its coverage by
// ExcludingTask, so the only thing this can affect is a LATER encounter with the
// same region, and only if the derivation succeeds there.
//
// Every outcome is emitted, including refusal and duplication. A round that
// writes nothing must be as visible as one that writes something, or the
// experiment measuring this loop would only ever see its successes.
func (e *Engine) recordClosureQuestion(taskID, condition string, d architectureDecision,
	start certifiedStart, model string, round int) {

	graph := map[string]string{}
	if a := start.workspace.GraphAuthority; a != nil {
		graph = map[string]string{
			"verdict": a.Verdict, "state": a.State,
			"freshness": string(a.GraphFreshnessState), "seed": string(a.SeedState),
			"provenance":         a.BuildProvenanceState,
			"graph_build_commit": a.GraphBuildCommit, "source_repo_commit": a.SourceRepoCommit,
		}
	}
	receipt := derived.InferenceReceipt{
		ModelName:        model,
		InputGraphDigest: derived.GraphDigest(graph),
		InputGraphState:  graph["graph_build_commit"],
		OriginTask:       taskID,
		OriginGap:        condition,
		Round:            round,
		Region:           append([]string(nil), d.Files...),
		ClosureBudget:    closureBudget,
		Nondeterminism:   derived.LLMNondeterminism,
	}
	defer func() {
		if err := derived.AppendReceipt(filepath.Join(e.Repo.Root, derivedReceiptsPath), receipt); err != nil {
			e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status,
				"could not record the inference receipt: "+err.Error(), nil))
		}
	}()

	if d.ProposedRecipe == nil {
		receipt.Outcome = derived.OutcomeNoProposal
		e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status,
			"the closure round proposed no checkable question for this region", nil))
		return
	}
	r := *d.ProposedRecipe
	// Provenance is stamped HERE, never taken from the provider: an agent that
	// could author its own provenance could make an invented question look like
	// the product of an investigation that never happened.
	r.Provenance = nil
	receipt.CandidateDigest = derived.DigestOf(r)
	receipt.CandidateID = r.Identity()

	added, err := derived.Append(
		filepath.Join(e.Repo.Root, derivedRecipesPath), r,
		derived.Provenance{
			OriginTask: taskID,
			OriginGap:  condition,
			Region:     append([]string(nil), d.Files...),
			WrittenBy:  "closure_round",
		}, d.Files)
	switch {
	case err != nil:
		receipt.Outcome, receipt.Detail = derived.OutcomeRefused, err.Error()
		e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status,
			"the proposed question was refused: "+err.Error(), nil))
	case !added:
		receipt.Outcome = derived.OutcomeDuplicate
		e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status,
			"this question is already recorded; not duplicating it: "+r.String(), nil))
	default:
		receipt.Outcome = derived.OutcomeRecorded
		e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status,
			"recorded a question for the next encounter with this region: "+r.String(), nil))
	}
}

// gapClosurePrompt turns a bounded knowledge gap into work instead of an
// interruption.
//
// The three refusals in it are the whole safety argument.
//
// It must not invent. An agent that meets a blind spot, writes a rule that
// removes the blind spot, and thereby obtains authority has automated
// self-approval — the graph becomes a mirror, and governance certifies
// whatever the agent last asserted. So closure establishes what the repository
// ALREADY shows and records it through `sensei propose`, which writes a
// candidate awaiting review rather than canonical knowledge. The agent cannot
// promote its own proposal.
//
// It must not treat closure as permission. Establishing knowledge says nothing
// about whether the original change may proceed; governance runs again from the
// top over whatever the graph then holds.
//
// It must not manufacture success. A gap that cannot be closed from available
// evidence is reported as unclosed, which is a real and useful answer — and
// the one that keeps this from becoming a machine for making warnings
// disappear.
func gapClosurePrompt(original string, d architectureDecision, condition string) string {
	return fmt.Sprintf(`%s

BOUNDED KNOWLEDGE GAP — CLOSE IT, DO NOT ROUTE IT:
Sensei did not refuse this work and no human owns it. It reported that the
knowledge needed to govern it is incomplete:

    %s

This is not permission to proceed, and it is not permission to experiment. It
is an instruction to establish what is already knowable, after which governance
runs again over what the graph then holds.

DO THIS, IN THIS ORDER:
 1. Read the actual source of the planned files, their tests, their callers and
    their package documentation. Prefer evidence that already exists over
    anything you would have to reason your way to.
 2. Check whether the knowledge exists elsewhere in the repository already and
    was simply not surfaced here — a sibling file, an existing contract, a
    scar, a required test, a header comment that states the rule.
 3. Fold what you established back into the plan as CLAIMS with
    source="repository", each naming the file and symbol it was read from. A
    premise you have now verified is no longer an inferred one, and that is a
    gap you can close here in full.

YOU ARE READ-ONLY IN THIS STAGE. Do not try to write a proposal: this role runs
without write permission, deliberately, so that investigation cannot become
authorship. You may not establish that anything is TRUE — how asserted knowledge
enters the graph without the graph becoming a mirror of what you asserted is an
open question (dq.closure_knowledge_admission), and it is not settled.

WHAT KIND OF GAP THIS IS, AND WHAT CLOSES IT. This gap is GRAPH COVERAGE: the
graph does not vouch for this region. Verifying your premises from the
repository is required above, and it does NOT close this gap -- the graph does
not read your claims. The gap closes only when a mechanical derivation over
this region succeeds on a later run, and the only thing that causes such a
derivation is a question left behind now.

SO LEAVE THE CHECKABLE PART BEHIND, WHETHER OR NOT YOU CONSIDER THE GAP CLOSED,
AND WHETHER YOU PROCEED OR ESCALATE. Return "proposed_recipe": a single
CHECKABLE relationship you found worth verifying mechanically in this region.
You are choosing WHERE TO LOOK. You are not stating what is so, and you gain
nothing by writing it: this task still stops, and the question cannot cover the
run that proposed it. Only a later derivation can turn it into coverage, and
only if the relationship actually holds there.

Three kinds are answerable. Anything else derives UNKNOWN forever, which is
writing nothing while looking like accumulation:

  {"kind":"field_access_under_lock","dir":"<pkg dir>","type":"<type name>",
   "field":"<field name>","lock":"<lock field name>",
   "why":"<what you read that made this worth asking>"}

  {"kind":"command_invocation_confined_to","command":"<exe>","owner":"<pkg dir>",
   "search_paths":["<dir>","<dir>"],"why":"..."}

  {"kind":"state_mutation_confined_to_owner","dir":"<declaring pkg dir>","type":"<exported type>",
   "field":"<exported field>","search_paths":["<dir>"],"why":"..."}

The question must be about the region you were asked to investigate.

A WRONG QUESTION IS SAFE AND A DISHONEST ONE IS NOT. If the relationship turns
out not to hold, the derivation says so and nothing is anchored — that costs one
derivation and misleads no one. So propose the check you actually think matters,
not the one most likely to pass. Omit the field entirely if nothing mechanical
would have helped; proposing nothing is a legitimate and common outcome.

DO NOT:
 - invent a rule so that the warning goes away. A claim that is not supported by
   something already in the repository is worse than the gap: the gap is honest.
 - assert that the region is safe. Absence of a finding is not a finding.
 - restate an inference as a repository claim. If nothing in the repository
   shows it, its source is still "inference".

IF THE GAP CANNOT BE CLOSED from evidence available to you, say so plainly and
return decision="escalate" with a claim naming exactly what could not be
established and what evidence would be needed. An unclosable gap reported
honestly is a correct outcome. A gap papered over is not.

THE PLAN THAT MET THE GAP:
%s

Return ONLY architecture JSON.`, original, condition, d.Plan)
}

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
	// The base was established at the start gate, before the workflow could
	// perturb its own repository. Re-establishing here would re-observe a tree
	// this run has already written to.
	identity := tc.Identity
	if strings.TrimSpace(identity.BaseSHA) == "" {
		fail(errors.New("no candidate identity was established for this task"))
		return
	}

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
		if err != nil && errors.Is(err, errStructural) {
			// The candidate is kept -- it holds real work -- and the run ends
			// with the structural reason. Another executor would receive the
			// same unauditable candidate and fail the same way (#89).
			state.OpenFindings(openFindings(review, audit, err))
			_ = state.Save(e.Repo.Root)
			fail(err)
			return
		}
		if err != nil {
			// A prospective grant authorizes one exact creation shape. Once its
			// post-creation inspection refutes that shape, the run is terminal:
			// a later implementor has no authority to reinterpret or retry it.
			// All other candidate failures retain the ordinary handoff path.
			if isProspectiveSurfaceRefutation(err) {
				fail(err)
				return
			}
			failures = append(failures, worker.Name+": "+err.Error())
			// The candidate stays: it holds real work, and the reviewer's
			// unresolved findings travel with it to whoever picks it up next.
			state.Phase = taskstate.Revising
			state.Evidence = tc.EvidenceSnapshot
			state.OpenFindings(openFindings(review, audit, err))
			_ = state.Save(e.Repo.Root)
			handoff := handoffPacket(state, roles.Binding{TaskID: taskID, BaseSHA: identity.BaseSHA},
				config.DisplayName(worker.Name), start.GraphBuildCommit(), e.Config.Workflow.ReviewCycles, e.Config.Workflow.ReviewCycles)
			if err := handoff.Continuity(roles.Binding{TaskID: taskID, BaseSHA: identity.BaseSHA}); err != nil {
				// A handoff that does not continue this task would start the
				// next worker over on the same branch, which looks like progress
				// and is not.
				fail(fmt.Errorf("the candidate could not be handed on: %w", err))
				return
			}
			carried = handoff.Render()
			e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.HandoffCreated,
				config.DisplayName(worker.Name)+" did not converge; the candidate and its unanswered findings pass to the next bounded worker",
				handoff))
			continue
		}
		plan = finalPlan
		if accepted {
			state.Phase = taskstate.Accepted
			state.Evidence = tc.EvidenceSnapshot
			state.OpenFindings(nil)
			_ = state.Save(e.Repo.Root)
			e.reportUndeliveredNotes(taskID)

			// A read-only plan produced findings and no candidate. There is
			// nothing to publish and nothing to retain: offering a pull request
			// for an empty branch would ask the human to land nothing, and
			// keeping the worktree would leave a directory that means nothing in
			// particular -- which is the state candidate dispositions exist to
			// remove.
			if tc.Mode == ModeInspect {
				e.reportOutcome(ctx, "success", task, "read-only plan completed; findings are in the transcript")
				e.disposeIfEmpty(ctx, taskID, identity, tc, workspace,
					"read-only plan: the candidate was never meant to hold work")
				e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.WorkflowCompleted,
					"read-only plan completed with no change to the repository", map[string]any{
						"implementor": worker.Name,
						"plan":        plan,
						"mode":        ModeInspect,
					}))
				return
			}

			published := e.offerPullRequest(ctx, taskID, tc, workspace, worker.Name)

			// A question the human left standing, or a run they stopped, has
			// settled nothing. Emitting a completion here is what stranded the
			// candidate: FindInterrupted reads WorkflowCompleted as terminal,
			// so the deferred question was filtered out of /resume immediately
			// after the interface promised to ask it again.
			if !published.Settled() {
				e.resolveCandidate(taskID, identity, tc, candidate.Resolution{
					Disposition: candidate.Resumable,
					Reason:      "accepted by review; the publication decision is still open",
				})
				if published.State == stopped {
					e.emit(event.New(e.SessionID, taskID, event.SourceUser, event.WorkflowStopped,
						"stopped while the publication decision was open; the candidate is left as it stands", nil))
				}
				return
			}

			// Retention here is a decision with a reason, not the absence of a
			// cleanup. Which reason depends on what actually happened, because
			// recording an opened pull request as unpublished contradicts both
			// Git and GitHub.
			disposition := candidate.Resolution{
				Disposition: candidate.Retained,
				Reason:      "accepted by review and unpublished; landing it is the human's decision",
			}
			outcome, summary := "success", "candidate ready for governed admission"
			switch published.State {
			case opened:
				disposition.Reason = "accepted by review and published for a human to land: " + published.Result.URL
				summary = "candidate published for a human to land, not merged and not admitted"
			case failed:
				disposition.Reason = "accepted by review; publication was attempted and did not complete"
				if effects := published.Result.Effects(); effects != "" {
					disposition.Reason += " (" + effects + ")"
				}
				outcome, summary = "failure", "publication did not complete: "+published.Err.Error()
			}
			e.reportOutcome(ctx, outcome, task, summary)
			e.resolveCandidate(taskID, identity, tc, disposition)
			// Exactly one terminal event. A failed publication used to emit
			// WorkflowFailed and then WorkflowCompleted for the same run.
			kind := event.WorkflowCompleted
			if published.State == failed {
				kind = event.WorkflowFailed
			}
			e.emit(event.New(e.SessionID, taskID, event.SourceSystem, kind, summary, map[string]any{
				"workspace":   workspace,
				"implementor": worker.Name,
				"plan":        plan,
				"review":      review,
				"audit":       audit,
				"publication": string(published.State),
			}))
			return
		}
	}

	// Nothing converged. Whether the candidate survives that is decided by what
	// it contains, not by how the run ended: a candidate holding work is kept
	// because deleting it would destroy something recoverable, and one holding
	// nothing is removed because keeping it would leave a directory of things
	// that mean nothing in particular. Neither branch is a default.
	e.reportUndeliveredNotes(taskID)
	e.disposeIfEmpty(ctx, taskID, identity, tc, workspace,
		"no bounded implementor converged and the candidate holds no work")
	fail(fmt.Errorf("no bounded implementor produced an acceptable candidate: %s", strings.Join(failures, " | ")))
}

func isProspectiveSurfaceRefutation(err error) bool {
	return err != nil && (strings.HasPrefix(err.Error(), "prospective surface refuted:") || strings.HasPrefix(err.Error(), "test edit refuted:"))
}

// candidateEvidence is what survives a candidate, assembled from what the run
// actually observed rather than from what it intended.
func candidateEvidence(identity candidate.Identity, tc *taskContext) candidate.Evidence {
	snapshot := tc.EvidenceSnapshot
	return candidate.Evidence{
		BaseSHA:        identity.BaseSHA,
		DiffBytes:      snapshot.DiffBytes,
		ChangedPaths:   snapshot.ChangedPaths,
		AuditVerdict:   snapshot.AuditVerdict,
		AuditDetail:    snapshot.AuditDetail,
		ProducedNoWork: snapshot.DiffBytes == 0 && len(snapshot.ChangedPaths) == 0,
	}
}

// observation is what the candidate itself says it holds, read at the moment a
// decision about deleting it is made.
type observation struct {
	DiffBytes    int
	ChangedPaths []string
	// Err is why the candidate could not be read. It is carried rather than
	// returned separately because "I could not look" is a distinct answer from
	// "I looked and it was empty", and the disposal path must not collapse the
	// two.
	Err error
}

// HoldsWork answers the only question disposal may act on.
//
// An unreadable candidate holds work. That is not a guess about its contents:
// deletion is irreversible and observation failed, so the branch that destroys
// something must not be reachable from an answer nobody established.
func (o observation) HoldsWork() bool {
	return o.Err != nil || o.DiffBytes > 0 || len(o.ChangedPaths) > 0
}

// observeCandidate reads the candidate worktree against the base it was cut
// from.
//
// Disposal used to consult tc.EvidenceSnapshot, which is written only after
// validation and a decodable Sensei audit. Every earlier exit -- a validation
// error, an audit transport failure, an undecodable audit, a read-only plan
// that edited -- left that snapshot zeroed while the worktree held real work,
// and the zero was read as "the candidate holds no work". The worktree and
// branch were then deleted and that sentence was recorded as the reason.
//
// The candidate is therefore asked directly. It is the only source that is
// correct at every exit, because it is the thing being destroyed.
func observeCandidate(ctx context.Context, workspace, baseSHA string) observation {
	if strings.TrimSpace(workspace) == "" || strings.TrimSpace(baseSHA) == "" {
		return observation{Err: fmt.Errorf("candidate identity is incomplete: workspace %q base %q", workspace, baseSHA)}
	}
	// Disposal is reached by the paths that cancel this context -- a stop, a
	// timeout -- so inheriting the cancellation would make every interrupted
	// run unreadable, and an unreadable candidate is retained. The leftovers
	// automatic cleanup exists to prevent would come back through the door
	// marked safety. Reading a worktree is a local, bounded operation that is
	// still correct after the work it belonged to was abandoned.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	diff, err := gitx.Repo{Root: workspace}.CandidateDiff(ctx, baseSHA)
	if err != nil {
		return observation{Err: err}
	}
	diff = strings.TrimSpace(diff)
	if diff == "" {
		return observation{}
	}
	return observation{DiffBytes: len(diff), ChangedPaths: changedPaths(diff)}
}

// resolveCandidate records a terminal disposition and says so out loud.
//
// A disposition that fails to record is reported rather than swallowed: the
// whole point is that a candidate can say why it is still here, and a silent
// failure returns it to meaning nothing.
func (e *Engine) resolveCandidate(taskID string, identity candidate.Identity, tc *taskContext, r candidate.Resolution) (candidate.Identity, bool) {
	r.Evidence = candidateEvidence(identity, tc)
	resolved, err := identity.Resolve(e.Repo.Root, r)
	if err != nil {
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status,
			"candidate disposition not recorded: "+err.Error(), nil))
		return identity, false
	}
	e.emit(event.New(e.SessionID, taskID, event.SourceGit, event.CandidateResolved, resolved.Resolution.Summary(), resolved.Resolution))
	return resolved, true
}

// disposeIfEmpty removes a candidate that produced nothing, and keeps one that
// produced something.
//
// The asymmetry is deliberate and is the safety property: automatic removal is
// only ever reached by a candidate whose own recorded evidence says it holds no
// work. Anything else is retained as resumable, with the reason attached, for a
// person or a later run to dispose of deliberately.
//
// Evidence is written before git is touched. If the removal then fails, the
// record says the disposition was disposed and the worktree is still present,
// which is a fact somebody can act on — where deleting first and recording
// second loses precisely the case that needs explaining.
func (e *Engine) disposeIfEmpty(ctx context.Context, taskID string, identity candidate.Identity, tc *taskContext, workspace, reason string) {
	// The candidate is read now, not recalled. See observeCandidate.
	seen := observeCandidate(ctx, workspace, identity.BaseSHA)
	if seen.HoldsWork() {
		kept := "the run did not converge and the candidate holds work that resumable state references"
		if seen.Err != nil {
			kept = "the candidate could not be read, so it is kept rather than removed on an unestablished claim: " + seen.Err.Error()
			e.emit(event.New(e.SessionID, taskID, event.SourceGit, event.Status, kept, nil))
		} else {
			e.emit(event.New(e.SessionID, taskID, event.SourceGit, event.Status,
				fmt.Sprintf("candidate kept so the work is not lost (%d bytes across %d file(s)): %s",
					seen.DiffBytes, len(seen.ChangedPaths), workspace), nil))
			// The snapshot is what gets recorded, so it must agree with what
			// was just seen. A retained candidate described as holding nothing
			// is the same false sentence in the other direction.
			if tc.EvidenceSnapshot.DiffBytes == 0 && len(tc.EvidenceSnapshot.ChangedPaths) == 0 {
				tc.EvidenceSnapshot.DiffBytes = seen.DiffBytes
				tc.EvidenceSnapshot.ChangedPaths = seen.ChangedPaths
			}
		}
		e.resolveCandidate(taskID, identity, tc, candidate.Resolution{
			Disposition: candidate.Resumable,
			Reason:      kept,
		})
		return
	}

	resolved, ok := e.resolveCandidate(taskID, identity, tc, candidate.Resolution{
		Disposition: candidate.Disposed,
		Reason:      reason,
	})
	if !ok {
		// The record refused, so nothing is removed. An unrecorded disposal is
		// the gap this mechanism exists to prevent.
		return
	}
	r := *resolved.Resolution
	if err := e.Repo.RemoveWorktree(ctx, workspace); err != nil {
		e.emit(event.New(e.SessionID, taskID, event.SourceGit, event.Status,
			"candidate worktree not removed: "+err.Error(), nil))
	} else {
		r.WorktreeRemoved = true
	}
	if branch := strings.TrimSpace(identity.Branch); branch != "" && r.WorktreeRemoved {
		if err := e.Repo.DeleteBranch(ctx, branch); err != nil {
			e.emit(event.New(e.SessionID, taskID, event.SourceGit, event.Status,
				"candidate branch not deleted: "+err.Error(), nil))
		} else {
			r.BranchRemoved = true
		}
	}
	if _, err := resolved.Resolve(e.Repo.Root, r); err != nil {
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status,
			"candidate disposition not updated after removal: "+err.Error(), nil))
	}
}

// resumeAuthority re-asks a Level-3 question that was deferred, exactly as it
// was asked.
//
// Nothing is re-derived on the way in: no preflight, no start gate, no router.
// That is the whole point. The router already exercised authority
// classification and produced this condition; re-running it against a graph
// that may have moved could produce a different question, and answering a
// different question would satisfy a boundary nobody was asked about. The
// question is settled history. What happens after it is answered is not: the
// work re-derives from current evidence, and the recorded answer is honoured
// there by the same mechanism that stops the router asking twice in one task.
func (e *Engine) resumeAuthority(ctx context.Context, task session.Interrupted) {
	var deferred DeferredAuthority
	if err := json.Unmarshal(task.AwaitingAuthority, &deferred); err != nil {
		e.emit(event.New(e.SessionID, task.TaskID, event.SourceSystem, event.WorkflowFailed,
			"the deferred authority question could not be read back, so it cannot be asked again: "+err.Error(), nil))
		return
	}
	if len(deferred.Decision.Options) == 0 {
		e.emit(event.New(e.SessionID, task.TaskID, event.SourceSystem, event.WorkflowFailed,
			"the deferred authority question carried no options, so there is nothing to answer", nil))
		return
	}

	// Sensei is started only to persist the answer if one is given. It is not
	// consulted about the question.
	sc, err := sensei.Start(ctx, e.Repo.Root, e.Config.Sensei.Command, e.Config.Sensei.Args)
	if err != nil {
		e.emit(event.New(e.SessionID, task.TaskID, event.SourceSystem, event.WorkflowFailed,
			fmt.Errorf("start Sensei: %w", err).Error(), nil))
		return
	}
	defer sc.Close()

	choice, err := e.awaitChoice(ctx, sc, task.TaskID, deferred.Condition, deferred.Domain, deferred.BaseSHA,
		deferred.Decision, deferred.Decision.Options)
	if err != nil {
		// Deferred again, or stopped at the boundary. Either way the question
		// stands and the workflow does not move past it.
		if !errors.Is(err, errAuthorityDeferred) {
			e.emit(event.New(e.SessionID, task.TaskID, event.SourceSystem, event.WorkflowFailed, err.Error(), nil))
		}
		return
	}
	e.emit(event.New(e.SessionID, task.TaskID, event.SourceSystem, event.Status,
		"authority decision answered on resume; continuing the task: "+choice, nil))
	e.execute(ctx, task.TaskID, task.Task)
}

// Resume continues a task that was interrupted after its plan was approved. The
// candidate worktree still holds the work, so this re-enters the implementation
// stage with the reviewer's last findings rather than re-deciding the plan.
func (e *Engine) Resume(ctx context.Context, task session.Interrupted) string {
	go func() {
		// A resumed task keeps the mode it was running in. Resumption is not a
		// new entry point a person chose, so its provenance says so.
		e.announceMode(task.TaskID, governedMode(ResumedGoverned))
		if len(task.AwaitingAuthority) != 0 {
			e.resumeAuthority(ctx, task)
			return
		}
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

		start, err := certifyStart(workspaceStatus, preflight, repositoryHead(ctx, e.Repo))
		if err != nil {
			e.emit(event.New(e.SessionID, task.TaskID, event.SourceSensei, event.Status, err.Error(), preflight.Structured))
			e.reportOutcome(ctx, "blocked", task.Task, err.Error())
			fail(err)
			return
		}

		// A resumed task already has a base recorded. Establish loads it rather
		// than re-deriving one, which is what keeps the base immutable across a
		// restart -- and it must not re-check cleanliness, because the tree may
		// legitimately hold this task's own earlier governance writes.
		identity, ok, idErr := candidate.Load(e.Repo.Root, task.TaskID)
		if idErr != nil {
			fail(idErr)
			return
		}
		if !ok {
			fail(fmt.Errorf("cannot resume %s: no candidate identity was recorded for it", task.TaskID))
			return
		}

		// Who authored the bound is re-established from the session record
		// before anything could revise it. A supplied plan whose record cannot
		// be reconstructed is not resumed under the architect instead.
		bound, err := e.restorePlanBound(task)
		if err != nil {
			fail(err)
			return
		}
		// The prospective authorization is restored from the record it was
		// written to, bound to the candidate's pinned base. Routing does not
		// re-run on resume, so it is the only source of the facts the
		// post-creation inspection checks against (sensei#312 cycle 3).
		// Existing-test edit authority is RE-ESTABLISHED, not restored: the
		// grants are recomputed from the pinned world through the same
		// derivation and predicate routing used, and the record must match
		// them exactly, or the resume refuses (sensei-code#101 review).
		// Side-effect free: a resume computes and compares; it never records.
		// Recording here minted authority on a SECOND resume, which read the
		// first resume's write as the run's own record.
		recomputed, _ := e.coverageAtWorld(ctx, task.TaskID, bound.Files, bound.Prospective)
		if err := e.restoreTestEditGrants(task, recomputed.edits, bound.Files, identity.BaseSHA); err != nil {
			fail(err)
			return
		}
		if err := e.restoreProspectiveGrants(task, bound.Prospective, identity.BaseSHA); err != nil {
			fail(err)
			return
		}
		tc := taskContext{
			Task:            task.Task,
			Identity:        identity,
			Conversation:    e.conversationSoFar(task.Task, 40),
			WorkspaceStatus: firstText(workspaceStatus),
			Preflight:       firstText(preflight),
			Rationale:       bound.Rationale,
			Steps:           bound.Steps,
			Domain:          start.Domain(),
			Mode:            bound.Mode,
			Consequences:    bound.Consequences,
			Invariants:      bound.Invariants,
			Prospective:     bound.Prospective,
			PlanSource:      bound.Source,
			PlanDigest:      task.PlanDigest,
		}
		carried := ""
		if r := strings.TrimSpace(task.Review); r != "" {
			carried = "This candidate was interrupted before it converged. Its changes are already present.\n\nThe last review said:\n" + r
		}
		e.emit(event.New(e.SessionID, task.TaskID, event.SourceSystem, event.Status,
			"resuming the interrupted candidate rather than starting over", nil))
		e.implement(ctx, sc, start, task.TaskID, &tc, bound.Plan, carried, fail)
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
func (e *Engine) answeredConditions(taskID string) []authority.Resolution {
	var out []authority.Resolution
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
		// A revise answer refuses this plan and leaves the condition open, so
		// the next plan is routed on its own merits. Recording it here would
		// make "require another design" unsatisfiable against a condition that
		// is a property of the region: every redesign reaches it again and is
		// refused by the human's own request for a redesign.
		if !res.Outcome.Settles() {
			continue
		}
		if strings.TrimSpace(res.Condition) != "" {
			out = append(out, res)
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
func (e *Engine) applyAnsweredCondition(taskID, condition string, scope ...string) (authorized, asked bool) {
	// Matched on the plan as well as the question. A condition is a property of
	// the region, not of the work: reusing an answer across plans let one yes
	// authorize a later plan touching entirely different files.
	//
	// Coverage is subset-tolerant, so a re-plan that stays inside what was
	// authorised is not a new question. Exact matching re-asked the human the
	// identical thing because the architect's file list shifted by a file
	// between rounds.
	//
	// A refusal that covers this plan governs over an authorisation that also
	// covers it. Both were given about a region containing this work, and
	// between "you may" and "you may not" the only safe reading is the refusal.
	for _, res := range e.answeredConditions(taskID) {
		if !res.Covers(condition, scope) {
			continue
		}
		if !res.Outcome.Permits() {
			return false, true
		}
		authorized, asked = true, true
	}
	return authorized, asked
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
	// The baseline is created lazily and only when a check has already failed:
	// attribution costs a second checkout and a second execution, and is worth
	// paying only for a result something is about to act on.
	var baselinePath string
	runner := validation.Runner{
		Workspace: repo.Root,
		Permits:   permits,
		Baseline: func() (string, error) {
			if baselinePath != "" {
				return baselinePath, nil
			}
			if strings.TrimSpace(base) == "" {
				return "", errors.New("no recorded base commit to compare against")
			}
			path, err := e.Repo.CreateWorktreeAt(ctx, taskID+"-baseline", base)
			if err != nil {
				return "", err
			}
			baselinePath = path
			return path, nil
		},
	}
	defer func() {
		if baselinePath != "" {
			_ = e.Repo.RemoveWorktree(ctx, baselinePath)
		}
	}()

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
	// The formatter's own evidence was discarded above; this proves formatting
	// holds for the bytes about to be reviewed, bound to the final digest.
	checks = append(checks, checksOf(validation.Format, e.Config.Validation.FormatVerify)...)
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
		out = append(out, validation.Check{
			Kind: kind, Command: c.Command, Args: c.Args,
			Mutates:      kind == validation.Format && !c.FailIfOutput,
			FailIfOutput: c.FailIfOutput,
		})
	}
	return out
}

// repositoryHead reads HEAD for the start gate, returning empty rather than
// failing: the gate's other checks are still worth running when git is
// unreadable, and an empty head simply skips the snapshot comparison.
func repositoryHead(ctx context.Context, repo gitx.Repo) string {
	head, err := repo.Head(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(head)
}

// observationFindings renders what an audit found, in the vocabulary the run
// actually established rather than as a plan.
//
// The architect returns a plan-shaped decision because that is the only shape
// the protocol has. In the observation lane there is nothing to implement, so
// the plan text is the account of what was inspected and the claims are the
// findings -- each already carrying whether it came from the repository, the
// graph, or the model's own inference.
func observationFindings(d architectureDecision, degraded []string, wroteToWorkspace string) string {
	var b strings.Builder
	b.WriteString("Observed. Nothing was admitted.\n")
	// A provider that wrote is not a safety failure -- the workspace is thrown
	// away -- but it contradicts the assumption the lane is configured on, so
	// it is surfaced rather than swallowed.
	if strings.TrimSpace(wroteToWorkspace) != "" {
		b.WriteString("\nThe configured architect WROTE to the disposable observation workspace. " +
			"Nothing left it and the governed repository is unchanged, but this provider is not " +
			"read-only and the lane assumes it is:\n")
		for _, line := range strings.Split(strings.TrimSpace(wroteToWorkspace), "\n") {
			b.WriteString("  ~ " + strings.TrimSpace(line) + "\n")
		}
	}
	// First, because it qualifies everything below it that came from the graph.
	if len(degraded) != 0 {
		b.WriteString("\nThe graph did not certify itself for this run. Findings sourced from it " +
			"are reported as read, not as established:\n")
		for _, d := range degraded {
			b.WriteString("  ! " + d + "\n")
		}
	}
	if s := strings.TrimSpace(d.Summary); s != "" {
		b.WriteString("\n" + s + "\n")
	}
	if len(d.Files) != 0 {
		b.WriteString("\nRead:\n")
		for _, f := range d.Files {
			b.WriteString("  " + f + "\n")
		}
	}
	var observed, inferred []Claim
	for _, c := range d.Claims {
		if strings.EqualFold(strings.TrimSpace(c.Source), "inference") {
			inferred = append(inferred, c)
			continue
		}
		observed = append(observed, c)
	}
	render := func(title string, cs []Claim) {
		if len(cs) == 0 {
			return
		}
		b.WriteString("\n" + title + "\n")
		for _, c := range cs {
			line := "  - " + strings.TrimSpace(c.Statement)
			if about := strings.TrimSpace(c.About); about != "" {
				line += "  [" + about + "]"
			}
			if src := strings.TrimSpace(c.Source); src != "" {
				line += "  (" + src + ")"
			}
			b.WriteString(line + "\n")
		}
	}
	render("Findings, from the repository or the graph:", observed)
	// Kept separate and kept last. An inferred claim is the model reasoning,
	// not something it read, and in the governed lane exactly this distinction
	// is what stops a plan. An audit may report one; it may not launder it into
	// a finding by listing it alongside the others.
	render("Model inferences — NOT established, and not evidence:", inferred)
	return strings.TrimRight(b.String(), "\n")
}

// observationPrompt turns the architect's planning brief into an inspection brief.
//
// The first observation run reported a PLAN to audit rather than the audit: the
// base prompt asks for steps and files, so a model given a read-only task
// answered with how it would carry the task out. Nothing had told it there
// would be no later stage, and in the governed lane there always is one -- the
// worker that does the reading is a separate agent further down.
//
// In this lane there is no worker. The architect is the only thing that will
// ever look at the code, so the plan IS the deliverable, and it has to be
// findings.
func observationPrompt(base string) string {
	return base + `

OBSERVATION RUN — READ THE FOLLOWING BEFORE YOU ANSWER.

This task entered through the observation lane. Nothing you say will be implemented:
no worker runs after you, no candidate worktree is created, no change is admitted,
and the run is verified to have left the working tree untouched before it may report
anything at all. You are not scoping work for someone else. You are the only agent
that will ever look at this code.

So do the inspection NOW, in this turn, and report what you actually found.

Still answer with "proceed" and the usual fields, but read them as an inspection:

- "plan" and "summary": what you INSPECTED and what you CONCLUDED, in the past tense.
  Not what you would do. A plan-shaped answer here produces a report that inspected
  nothing.
- "files": the files you actually opened.
- "claims": THE FINDINGS. This is the substance of the run. One claim per finding,
  each with its source:
    "repository"  you opened the file and read the thing you are describing.
                  Quote or name the exact symbol, comment, or line.
    "graph"       a Sensei tool returned it. Say which tool.
    "inference"   you reasoned to it and did not directly observe it.

The source field is load-bearing and it is checked. An "inference" is reported
separately and explicitly as NOT established, so putting a real observation there
buries it, and putting a guess under "repository" is a false statement about what
you read. Prefer fewer, verified findings over a long list.

Report a defect even when the fix looks obvious, and do not report the fix as though
it were done. If you found nothing, say so plainly — an audit that finds nothing is
a valid result, and inventing findings to look productive is the specific failure
this lane exists to avoid.`
}

// observe is the read-only lane, end to end.
//
// A path of its own rather than a guard inside the change workflow. Nothing
// here establishes a candidate identity, creates an admission record, runs a
// worker, or can exit through a completion branch, because none of that code is
// on this path at all.
//
// # Exactly what the two mechanisms establish
//
// Stated separately and narrowly, because the explanatory surface here has now
// outrun the mechanism twice: once when a post-hoc cleanliness check was called
// a boundary, and again when a detached worktree was described as stopping a
// write-capable provider from touching the governed checkout. Both times the
// observation lane read the comment and reported it as false.
//
//	isolated worktree   the provider's normal workspace is elsewhere
//	before/after witness the governed repository ended unchanged across the
//	                     state we measure
//	neither              filesystem confinement
//
// The witness is `git status --porcelain` on the governed root, taken before
// the observer starts and again after it finishes. It establishes that the
// measured state is the same at both moments. It does NOT establish that the
// provider was unable to write there, and it does not exclude a write followed
// by a restore, or a change to something porcelain does not report.
//
// The architect is a subprocess holding the ambient permissions of the user
// running Sensei Code. Nothing here constrains what such a process can reach;
// it constrains where it is POINTED, and it notices afterwards if the governed
// repository moved.
//
// Writes to the disposable workspace are measured rather than ignored. They
// harm nothing, and they are the observable signal that a configured provider
// is not as read-only as the lane assumes, so they are reported.
func (e *Engine) observe(ctx context.Context, sc *sensei.Client, start certifiedStart, taskID, task string, workspaceStatus sensei.ToolResult) error {
	head := repositoryHead(ctx, e.Repo)
	if strings.TrimSpace(head) == "" {
		return errors.New("observation cannot start: the repository HEAD could not be resolved, " +
			"so there is no commit to check out a disposable copy of")
	}
	// What the governed repository looked like BEFORE the observer ran.
	//
	// The check used to read `git status --porcelain` only afterwards, which
	// asks the wrong question twice over: it cannot tell a change this run made
	// from one that was already there, and it reported an already-dirty
	// repository as having been mutated by the observation. Hit for real while
	// re-auditing a retained candidate, whose fix is uncommitted by design.
	//
	// Comparing before and after answers the question that matters -- did THIS
	// run change the governed repository -- and it is still evidence rather
	// than confinement. The boundary is that the observer's working directory
	// is somewhere else; this is what detects a provider that went looking for
	// the governed checkout anyway.
	before, beforeErr := e.Repo.WorktreeIsCleanDetail(ctx, e.Repo.Root)
	if beforeErr != nil {
		return fmt.Errorf("observation cannot start: the governed repository could not be read, "+
			"so nothing could establish whether this run changed it: %w", beforeErr)
	}

	workspace, err := e.Repo.CreateObservationWorktree(ctx, taskID, head)
	if err != nil {
		return fmt.Errorf("observation workspace: %w", err)
	}
	// A backstop for the error paths below. The success path discards the
	// workspace explicitly, BEFORE the terminal event.
	discarded := false
	defer func() {
		if !discarded {
			_ = e.Repo.RemoveObservationWorktree(context.WithoutCancel(ctx), workspace)
		}
	}()

	domain := sensei.RepositoryDomain(workspaceStatus)
	conversation := e.conversationSoFar(task, 40)
	retrieved, repository, standing, history, consulted := e.architecturalContext(ctx, sc, domain, task)
	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.ContextConsulted, consulted.Render(), consulted))

	decision, err := e.resolveArchitectureIn(ctx, sc, start, taskID, task, observationPrompt(architecturePrompt(
		workspace, domain, config.DisplayName(e.Config.Architect.Name), task, conversation,
		firstText(workspaceStatus), "", retrieved, repository, standing, history)), workspace)
	if err != nil {
		return err
	}

	// The governed repository must be exactly as this run found it.
	after, afterErr := e.Repo.WorktreeIsCleanDetail(ctx, e.Repo.Root)
	if afterErr != nil {
		return fmt.Errorf("observation cannot report: the governed repository could not be checked: %w", afterErr)
	}
	if after != before {
		return fmt.Errorf("the governed repository changed during this observation; refusing to report it "+
			"as read-only. The observer was pointed at a disposable workspace, so either the configured "+
			"provider reached the governed checkout anyway or something else in this process wrote to it.\n"+
			"before:\n%s\nafter:\n%s", indentOrNone(before), indentOrNone(after))
	}

	var wrote string
	if clean, dirty, werr := e.Repo.WorktreeIsClean(ctx, workspace); werr == nil && !clean {
		wrote = dirty
	}

	// Discard BEFORE the terminal event, not in a defer.
	//
	// A deferred cleanup leaked every workspace: a headless caller returns from
	// its event loop the moment WorkflowObserved arrives and exits the process,
	// so the defer was racing process teardown and losing. Observed directly --
	// the first restructured run left
	// .sc2-worktrees/task-...-observe behind.
	if err := e.Repo.RemoveObservationWorktree(ctx, workspace); err != nil {
		return fmt.Errorf("observation workspace could not be discarded, so a read-only run left "+
			"state behind: %w", err)
	}
	discarded = true

	// Recorded before the terminal event, so a caller that acts on the outcome
	// can read them. Recording is not authorising: nothing here grants the
	// repair anything, and an ineligible finding is retained and reported
	// exactly like an eligible one -- it simply cannot become work.
	e.recordFindings(taskID, task, head, decision)

	e.emit(event.New(e.SessionID, taskID, event.SourceArchitect, event.ArchitectSpoke,
		observationFindings(decision, start.Degraded(), wrote), decision))
	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.WorkflowObserved,
		"observed and reported; nothing was admitted, the governed repository is unchanged, "+
			"and the observation workspace was discarded", nil))
	return nil
}

// recordFindings converts an observation's claims into durable findings.
//
// Kept on the engine rather than returned, because the observation lane is
// terminal: its caller sees an event stream, not a value. Findings() reads them
// back afterwards.
func (e *Engine) recordFindings(taskID, objective, world string, d architectureDecision) {
	var out []finding.Finding
	for _, c := range d.Claims {
		out = append(out, finding.New(taskID, world, objective, c.Statement, c.About,
			filesFor(c, d), d.Files, c.Source))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.findings == nil {
		e.findings = map[string][]finding.Finding{}
	}
	e.findings[taskID] = out
}

// filesFor decides which files a claim is about.
//
// A claim's About field is prose the observer wrote, so it is read for a path
// and never trusted to BE one: if it names a file the observation actually
// opened, that is the claim's subject. Otherwise the claim inherits the files
// the observation read, which is wider and therefore weaker, and the repair
// re-checks all of it anyway.
func filesFor(c Claim, d architectureDecision) []string {
	var named []string
	for _, f := range d.Files {
		if strings.Contains(c.About, f) || strings.Contains(c.Statement, f) {
			named = append(named, f)
		}
	}
	if len(named) != 0 {
		return named
	}
	return d.Files
}

// Findings returns what an observation established, for a caller deciding
// whether to open repair work.
func (e *Engine) Findings(taskID string) []finding.Finding {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]finding.Finding(nil), e.findings[taskID]...)
}

// indentOrNone renders a worktree status for a refusal message.
func indentOrNone(status string) string {
	if strings.TrimSpace(status) == "" {
		return "  (clean)"
	}
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(status, "\n"), "\n") {
		b.WriteString("  " + line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

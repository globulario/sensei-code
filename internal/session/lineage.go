package session

import (
	"fmt"
	"strings"

	"github.com/globulario/sensei-code/internal/event"
)

// THE CANONICAL SESSION LINEAGE OF A TASK (70B2a1, RULING-188).
//
// One physical task ledger, successive session identities. A session is one
// process's identity; a task outlives the process that began it. A fresh
// process that continues a task appends to the record that already holds the
// task -- the holder Store -- under its OWN SessionID, and it may do so only
// after one durable SessionLineageBound transition, written through
// Store.BindSessionLineage, made it the task's current session.
//
// The task's ROOT is its one TaskCreated record. The session that wrote it is
// the task's HolderSessionID: proven from the durable record, never taken from
// a caller. The lineage is an ORDERED CHAIN A -> B -> C: each transition names
// the task, the holder, the parent -- the tip the record establishes
// immediately before it -- and the current session that wrote it. A record
// without a root establishes no lineage at all.
//
// Lineage authorizes a later session to CONSUME the task's earlier records. It
// never rewrites who wrote them: a historical record stays owned by exactly
// the SessionID it was written under.

// SessionLineageBound is the durable kind of one session-lineage transition,
// declared with every other durable kind in package event.
const SessionLineageBound = event.SessionLineageBound

// SessionLineageRelationResume is the one canonical session-lineage relation:
// the current session continues the task its parent left. The vocabulary is
// closed and read by membership.
const SessionLineageRelationResume = "resume_continuation"

// SessionLineageBinding is the closed payload of one SessionLineageBound
// record.
type SessionLineageBinding struct {
	TaskID string `json:"task_id"`
	// HolderSessionID is the session that wrote the task's TaskCreated root.
	HolderSessionID string `json:"holder_session_id"`
	// ParentSessionID is the task's lineage tip immediately before this
	// transition.
	ParentSessionID string `json:"parent_session_id"`
	// CurrentSessionID is the fresh session the transition makes current; the
	// binding record is written under it.
	CurrentSessionID string `json:"current_session_id"`
	Relation         string `json:"relation"`
}

// SessionLineageSegment is one session of a task's lineage and the record
// position from which it is the task's current session.
type SessionLineageSegment struct {
	SessionID string
	From      int
}

// SessionLineage is the canonical session lineage of one task, as the record
// it was reconstructed from establishes it.
type SessionLineage struct {
	TaskID string
	// HolderSessionID is the session that wrote the task's root.
	HolderSessionID string
	// Root is the record position of the task's TaskCreated root.
	Root     int
	Segments []SessionLineageSegment
}

// Tip is the task's current session.
func (l SessionLineage) Tip() string {
	if len(l.Segments) == 0 {
		return ""
	}
	return l.Segments[len(l.Segments)-1].SessionID
}

// OwnerAt is the session that was the task's current session at record
// position at: the holder from the root until the first transition, each
// bound session from its own transition on. A position before the root has no
// owner: the root never projects ownership backwards onto what precedes it.
func (l SessionLineage) OwnerAt(at int) string {
	if len(l.Segments) == 0 || at < l.Segments[0].From {
		return ""
	}
	owner := l.Segments[0].SessionID
	for _, s := range l.Segments[1:] {
		if s.From > at {
			break
		}
		owner = s.SessionID
	}
	return owner
}

// Includes reports whether sessionID is a session of the lineage, historical
// or current.
func (l SessionLineage) Includes(sessionID string) bool {
	for _, s := range l.Segments {
		if s.SessionID == sessionID {
			return true
		}
	}
	return false
}

// BindingFor is the transition that would make current the task's next
// session: parented on the tip this lineage establishes and naming its proven
// holder. It refuses an empty current session and any session the lineage
// already names, the tip included: a session never continues itself and a
// historical session never becomes current again.
func (l SessionLineage) BindingFor(current string) (SessionLineageBinding, error) {
	switch {
	case len(l.Segments) == 0:
		return SessionLineageBinding{}, fmt.Errorf("%w: task %s has no root to continue", ErrNoTaskRoot, l.TaskID)
	case strings.TrimSpace(current) == "":
		return SessionLineageBinding{}, fmt.Errorf("%w: no current session is named to continue task %s", ErrSessionLineage, l.TaskID)
	case l.Includes(current):
		return SessionLineageBinding{}, fmt.Errorf("%w: session %q already belongs to task %s's lineage, whose tip is %q",
			ErrSessionLineage, current, l.TaskID, l.Tip())
	}
	return SessionLineageBinding{TaskID: l.TaskID, HolderSessionID: l.HolderSessionID, ParentSessionID: l.Tip(),
		CurrentSessionID: current, Relation: SessionLineageRelationResume}, nil
}

// TaskSessionLineage reconstructs the canonical session lineage of taskID
// from record, in record order, through the one validator every lineage
// consumer and appender shares (step). A record holding no TaskCreated root
// of the task is ErrNoTaskRoot. A second root, a malformed root, and any
// transition step refuses -- malformed, preceding the root, naming another
// task, holder or relation, not the system's, not written by the session it
// makes current, not continuing the tip at its position, or re-binding a
// session the lineage already names -- is ErrSessionLineage: a lineage that
// cannot be established is no lineage. So is any other record of the task at
// or after its root that is not a valid governed event (ValidGovernedEvent).
func TaskSessionLineage(record []event.Event, taskID string) (SessionLineage, error) {
	l, _, err := reconstructLineage(record, taskID)
	if err != nil {
		return SessionLineage{}, err
	}
	return l, nil
}

// reconstructLineage is TaskSessionLineage's one pass over the record. On
// refusal it also returns what that pass had validated before the record it
// refused -- the partial lineage, its root included when one preceded the
// refusal -- and the position of the refused record (-1 when the refusal is
// a record with no root at all). That prefix is never authority: it is
// returned only so ClassifyTaskHistory can keep records that precede the
// task's root distinguishable from the malformed lineage after it.
func reconstructLineage(record []event.Event, taskID string) (SessionLineage, int, error) {
	if strings.TrimSpace(taskID) == "" {
		return SessionLineage{Root: -1}, -1, fmt.Errorf("%w: no task is named", ErrSessionLineage)
	}
	l := SessionLineage{TaskID: taskID, Root: -1}
	for i, e := range record {
		if e.TaskID != taskID {
			continue
		}
		if e.Kind != event.TaskCreated && e.Kind != SessionLineageBound {
			// Every other record of the task at or after its root is
			// governed history the lineage authorizes consumption of: a
			// malformed one -- an unknown kind, a wrong source, missing
			// framing or a payload that is not its kind's -- leaves the task
			// with no establishable lineage, so nothing continues, binds or
			// appends over it. A record before the root has no task-session
			// owner and is left to ClassifyTaskHistory as pre-root.
			if l.Root >= 0 {
				if err := ValidGovernedEvent(e); err != nil {
					return l, i, fmt.Errorf("%w: a %s record of task %s at position %d is not a valid governed event: %w",
						ErrSessionLineage, e.Kind, taskID, i, err)
				}
			}
			continue
		}
		if err := l.step(e, i); err != nil {
			if !isSessionLineage(err) {
				err = fmt.Errorf("%w: %w", ErrSessionLineage, err)
			}
			return l, i, err
		}
	}
	if l.Root < 0 {
		return l, -1, fmt.Errorf("%w: the record holds no TaskCreated root of task %s", ErrNoTaskRoot, taskID)
	}
	return l, -1, nil
}

// validFraming is THE canonical framing test of a task's session event
// (RULING-193 A): it names its task, the session that wrote it and its kind,
// carries an event ID and a timestamp, and is attributed to a source of the
// closed vocabulary. Every consumer and appender of lineage authority applies
// it through step or authorize; none checks framing field by field of its
// own.
func validFraming(e event.Event) error {
	switch {
	case strings.TrimSpace(e.TaskID) == "":
		return fmt.Errorf("%w: a %s event names no task", ErrMalformedEvent, e.Kind)
	case strings.TrimSpace(string(e.Kind)) == "":
		return fmt.Errorf("%w: an event of task %s names no kind", ErrMalformedEvent, e.TaskID)
	case strings.TrimSpace(e.SessionID) == "":
		return fmt.Errorf("%w: a %s event of task %s names no session", ErrMalformedEvent, e.Kind, e.TaskID)
	case strings.TrimSpace(e.ID) == "":
		return fmt.Errorf("%w: a %s event of task %s carries no event ID", ErrMalformedEvent, e.Kind, e.TaskID)
	case e.Time.IsZero():
		return fmt.Errorf("%w: a %s event of task %s carries no timestamp", ErrMalformedEvent, e.Kind, e.TaskID)
	case !eventSources[e.Source]:
		return fmt.Errorf("%w: a %s event of task %s is attributed to %q, which is no source", ErrMalformedEvent, e.Kind, e.TaskID, e.Source)
	}
	return nil
}

// THE GOVERNED KIND REGISTRY (RULING-193 A). governedKinds is the closed,
// exhaustive vocabulary of the kinds a task's session event may have, read by
// membership: a task-bound event of a kind it does not name is malformed,
// whoever writes it. Each kind names, explicitly, the sources that may record
// it and the canonical structure of its payload. It is the one neutral
// semantic owner every appender (authorizeAppend), lineage reconstruction
// (step), classification (ClassifyTaskHistory) and history consumer
// (ValidGovernedEvent) applies; none re-derives it.
type kindRule struct {
	// sources may record the kind; none other may.
	sources map[event.Source]bool
	// payload is the canonical test of the kind's payload: given the payload
	// decoded as one object (nil when it is absent), it refuses one that is
	// not the kind's structure or misses a required member.
	payload func(e event.Event, m payloadMembers, present bool) error
	// shapes are the closed payload shapes of a kind whose payload is one
	// of them (closedKind), and absent reports that such a kind may also be
	// recorded with no payload. They are what payload enforces, kept on the
	// rule so the registry's own witnesses read what the Store enforces.
	shapes []shape
	absent bool
	// observation, when set, admits a payload no shape matches as a Sensei
	// tool's own observation (senseiObservation): a result whose members
	// the tool, not this registry, owns, recorded by Sensei alone.
	observation func(e event.Event, m payloadMembers) error
}

// closedKind is the rule of a kind recorded by srcs whose payload is exactly
// one of shapes -- or, when absent, none at all -- and then satisfies each of
// semantics: the kind's own conditions on the members its shape admits.
func closedKind(srcs map[event.Source]bool, absent bool, shapes []shape, semantics ...func(event.Event, payloadMembers) error) kindRule {
	return observedKind(srcs, absent, nil, shapes, semantics...)
}

// observedKind is closedKind for a kind whose payload is either one of shapes
// or, failing every shape, the Sensei tool observation observation admits.
func observedKind(srcs map[event.Source]bool, absent bool, observation func(event.Event, payloadMembers) error,
	shapes []shape, semantics ...func(event.Event, payloadMembers) error) kindRule {
	return kindRule{sources: srcs, shapes: shapes, absent: absent, observation: observation,
		payload: func(e event.Event, m payloadMembers, present bool) error {
			if !present && absent {
				return nil
			}
			err := exactlyOneOf(e, m, present, shapes...)
			if err != nil && present && observation != nil {
				if oerr := observation(e, m); oerr != nil {
					return fmt.Errorf("%w; nor is it a Sensei observation: %w", err, oerr)
				}
				return nil
			}
			if err != nil {
				return err
			}
			for _, check := range semantics {
				if err := check(e, m); err != nil {
					return err
				}
			}
			return nil
		}}
}

// payloadless is the rule of a kind recorded by srcs with no payload at all.
func payloadless(srcs map[event.Source]bool) kindRule {
	return kindRule{sources: srcs, payload: noPayload}
}

func sources(ss ...event.Source) map[event.Source]bool {
	out := make(map[event.Source]bool, len(ss))
	for _, s := range ss {
		out[s] = true
	}
	return out
}

var (
	// eventSources is the closed vocabulary of sources a session event may be
	// attributed to, and taskRootSources the subset a TaskCreated root may be:
	// the system that records a submission, and the user whose objective it
	// is. Both are read by membership.
	eventSources = sources(event.SourceSystem, event.SourceUser, event.SourceSensei, event.SourceArchitect,
		event.SourceReviewer, event.SourceClaude, event.SourceCodex, event.SourceGit, event.SourceTests)
	taskRootSources = sources(event.SourceSystem, event.SourceUser)
	systemOnly      = sources(event.SourceSystem)
	// roleTurnSources are the sources a role turn's runner reports under: the
	// architect's and the reviewer's, an implementer's provider (claude,
	// codex), and the system for an implementer of no named provider.
	roleTurnSources = sources(event.SourceArchitect, event.SourceReviewer, event.SourceClaude, event.SourceCodex, event.SourceSystem)
	// implementerSources are the sources an implementer's own report is
	// attributed to: its provider, or the system for one of no named provider.
	implementerSources = sources(event.SourceClaude, event.SourceCodex, event.SourceSystem)
	// statusSources are the parties that report progress: every source but
	// the test runner, which reports through validation records.
	statusSources = sources(event.SourceSystem, event.SourceUser, event.SourceSensei, event.SourceArchitect,
		event.SourceReviewer, event.SourceClaude, event.SourceCodex, event.SourceGit)
)

var governedKinds = map[event.Kind]kindRule{
	event.TaskCreated:         payloadless(taskRootSources),
	event.SessionLineageBound: closedKind(systemOnly, false, []shape{lineageTransitionShape}, semanticsOf(lineageTransitionPayload)),
	event.PlanProposed:        closedKind(sources(event.SourceArchitect, event.SourceSystem), false, []shape{planProposedShape}, semanticsOf(planProposedPayload)),
	event.DecisionRecorded:    payloadless(sources(event.SourceSensei)),
	event.GuidanceDelivered:   payloadless(sources(event.SourceUser)),
	event.InspectionReported:  payloadless(implementerSources),
	event.ReviewStarted:       closedKind(sources(event.SourceReviewer), false, []shape{reviewStartedShape}),
	event.ReviewFinding:       closedKind(sources(event.SourceReviewer), false, []shape{reviewFindingShape}, semanticsOf(reviewFindingPayload)),
	event.ReviewCompleted:     closedKind(sources(event.SourceReviewer), false, []shape{reviewCompletedShape}, semanticsOf(reviewCompletedPayload)),
	// The candidate's disposition is its owner's, Git's, decision
	// (candidateResolutionPayload): no other source records it.
	event.CandidateResolved:   closedKind(sources(event.SourceGit), false, []shape{candidateResolutionShape}, semanticsOf(candidateResolutionPayload)),
	event.RunReceipt:          closedKind(systemOnly, false, []shape{receiptShape}, semanticsOf(receiptPayload)),
	event.AuthorityRequired:   closedKind(sources(event.SourceArchitect), false, []shape{decisionShape}, semanticsOf(authorityRequiredPayload)),
	event.AuthorityResolved:   closedKind(sources(event.SourceUser), false, []shape{authorityAnswerShape, authorityResolutionShape}, semanticsOf(authorityAnswerPayload)),
	event.PlanAttemptStarted:  closedKind(systemOnly, false, []shape{planAttemptStartedShape}, semanticsOf(planAttemptStartedPayload)),
	event.PlanAttemptRefused:  closedKind(systemOnly, false, []shape{planAdmissionRefusalShape}, semanticsOf(planAdmissionRefusalPayload)),
	event.ProspectiveGranted:  closedKind(systemOnly, false, []shape{grantShape}, semanticsOf(grantPayload)),
	event.TestEditGranted:     closedKind(systemOnly, false, []shape{grantShape}, semanticsOf(grantPayload)),
	event.CheckpointPrepared:  closedKind(systemOnly, false, []shape{checkpointShape}, semanticsOf(checkpointPayload)),
	event.CheckpointCommitted: closedKind(systemOnly, false, []shape{checkpointShape}, semanticsOf(checkpointPayload)),
	// Standing-state run endings.
	event.WorkflowAwaitingAuthority:     closedKind(sources(event.SourceUser), false, []shape{awaitingAuthorityShape}, semanticsOf(awaitingAuthorityPayload)),
	event.WorkflowAwaitingReview:        closedKind(sources(event.SourceReviewer), false, []shape{awaitingReviewShape}, semanticsOf(awaitingReviewPayload)),
	event.WorkflowBlockedExternal:       closedKind(systemOnly, false, []shape{blockedExternalShape}, semanticsOf(blockedExternalPayload)),
	event.WorkflowNotConverged:          closedKind(systemOnly, false, []shape{notConvergedShape}, semanticsOf(notConvergedPayload)),
	event.WorkflowRestorationRefused:    closedKind(systemOnly, false, []shape{restorationRefusedShape}, semanticsOf(restorationRefusedPayload)),
	event.WorkflowBaseMovedRefused:      closedKind(systemOnly, false, []shape{baseMovedShape}, semanticsOf(baseMovedRefusedPayload)),
	event.WorkflowDirtyCanonicalRefused: closedKind(systemOnly, false, []shape{dirtyCanonicalShape}, semanticsOf(dirtyCanonicalRefusedPayload)),
	event.WorkflowPlanAdmissionRefused:  closedKind(systemOnly, false, []shape{planAdmissionRefusalShape}, semanticsOf(planAdmissionRefusalPayload)),
	event.ModeSelected:                  closedKind(systemOnly, false, []shape{modeSelectedShape}),
	event.ArchitectSpoke:                closedKind(sources(event.SourceArchitect), true, []shape{architectSpokeShape}),
	event.PullRequestOpened:             closedKind(sources(event.SourceGit), false, []shape{pullRequestShape}),
	event.RoleAssigned:                  closedKind(systemOnly, false, []shape{architectAssignedShape, reviewerAssignedShape}, roleAssignedSemantics),
	event.CandidateChanged:              closedKind(sources(event.SourceGit), false, []shape{candidateChangedShape}),
	event.CandidateArtifactExcluded:     closedKind(sources(event.SourceGit), false, []shape{artifactExcludedShape}),
	event.Output:                        closedKind(roleTurnSources, false, []shape{outputShape}),
	event.AgentStarted:                  closedKind(roleTurnSources, true, agentStartedShapes, transportSemantics),
	event.AgentFinished:                 closedKind(roleTurnSources, true, agentFinishedShapes, transportSemantics, agentOutcomeSemantics),
	event.Status:                        observedKind(statusSources, true, senseiObservation(""), statusShapes, statusSemantics),
	event.SenseiResult:                  observedKind(sources(event.SourceSensei), true, senseiObservation(""), nil),
	event.CandidateAudited:              observedKind(sources(event.SourceSensei), false, senseiObservation(operationAuditDiff), nil),
	event.CandidateNotAuditable:         observedKind(sources(event.SourceSensei), false, senseiObservation(operationAuditDiff), nil),
	event.ChangeReported:                closedKind(sources(event.SourceSensei), false, []shape{changeReportedShape}),
	event.ContextConsulted:              closedKind(systemOnly, false, []shape{contextConsultedShape}),
	event.HandoffCreated:                closedKind(systemOnly, false, []shape{handoffShape}),
	event.ReviewContradiction:           closedKind(systemOnly, false, []shape{contradictionShape}, reviewDecisionSemantics),
	event.ArchitectReconciliation:       closedKind(sources(event.SourceArchitect), false, []shape{reconciliationShape}, reconciliationSemantics),
	event.RoutineClassified:             closedKind(systemOnly, true, []shape{routineShape}),
	event.ValidationRun:                 closedKind(systemOnly, false, []shape{validationShape}),
	// Run endings: the system's, save those a person or the reviewer causes.
	event.WorkflowCompleted: closedKind(systemOnly, true, []shape{inspectCompletedShape, runSettledShape, planAdmissionRefusalShape},
		inspectSemantics, semanticsOf(planAdmissionRefusalIfOne)),
	event.WorkflowFailed: closedKind(systemOnly, true, []shape{runSettledShape, implementerIncompleteShape, obligationPersistenceFailedShape},
		checkpointStatusSemantics),
	event.WorkflowObserved: payloadless(systemOnly),
	event.WorkflowTimedOut: payloadless(systemOnly),
	event.WorkflowStopped:  payloadless(sources(event.SourceSystem, event.SourceUser)),
}

// roleAssignedSemantics: a role assignment names a role of the closed
// vocabulary.
func roleAssignedSemantics(e event.Event, m payloadMembers) error {
	if role, _ := m.str("role"); !validRole(role) {
		return fmt.Errorf("%w: a role assignment of task %s names role %s, which is no role", ErrMalformedEvent, e.TaskID, m["role"])
	}
	return nil
}

// semanticsOf adapts a kind's payload test to the semantics closedKind
// applies once the payload has matched one of the kind's shapes.
func semanticsOf(check func(event.Event, payloadMembers, bool) error) func(event.Event, payloadMembers) error {
	return func(e event.Event, m payloadMembers) error { return check(e, m, true) }
}

// authorityResolutionMembers are authority.Resolution's members as it
// marshals, with extra.
func authorityResolutionMembers(extra map[string]member) map[string]member {
	out := map[string]member{
		"task_id": req(tText), "session_id": req(tText), "domain": opt(tString), "base_sha": opt(tString),
		"decided_at": req(tText), "question": req(tText), "condition": req(tString), "option_id": req(tText),
		"option_label": req(tString), "scope": opt(tStrings), "outcome": req(tText), "state": req(tText),
		"candidate_path": opt(tString), "node_ids": opt(tStrings), "detail": opt(tString),
	}
	for k, m := range extra {
		out[k] = m
	}
	return out
}

// architectureDecisionMembers are the members of the architect's decision as
// the workflow marshals it (workflow.architectureDecision): its decision,
// summary and plan always, every other member when stated.
func architectureDecisionMembers(extra map[string]member) map[string]member {
	out := map[string]member{
		"decision": req(tString), "summary": req(tString), "plan": req(tString), "message": opt(tString),
		"steps": opt(tStrings), "consequences": opt(tString), "files": opt(tStrings), "mode": opt(tString),
		"related_invariants": opt(tStrings), "adjudication": opt(tString), "human_question": opt(tString),
		"recommendation": opt(tString), "options": opt(tArray), "claims": opt(tArray), "proposed_recipe": opt(tObjectOr),
		"prospective_surfaces": opt(tArray), "test_edits": opt(tArray), "premise_resolutions": opt(tArray),
		"declared_effects": opt(tArray),
	}
	for k, m := range extra {
		out[k] = m
	}
	return out
}

// THE SENSEI TOOL OBSERVATIONS. A Sensei answer is recorded as Sensei
// returned it -- its structured content, whose members Sensei owns and this
// registry does not close -- beside the one member sensei-code adds, the
// evidence envelope naming what was asked (evidence.Envelope). The envelope
// is closed: exactly its declared members, an operation of the closed
// vocabulary. An observation is admitted only from Sensei, only on the kinds
// whose rule names it, and no restoration consumer reads one as task state.
const (
	operationPreflight = "awareness_preflight"
	operationAuditDiff = "awareness_audit_diff"
)

var evidenceEnvelopeShape = shape{name: "an evidence envelope", members: map[string]member{
	"operation": req(tText), "request": req(tObjectOr), "revision": opt(tString), "graph_digest": opt(tString), "tool": opt(tString)}}

// senseiObservation admits a Sensei answer; when operation is named, its
// evidence envelope is required and records exactly that operation.
func senseiObservation(operation string) func(event.Event, payloadMembers) error {
	return func(e event.Event, m payloadMembers) error {
		if e.Source != event.SourceSensei {
			return fmt.Errorf("%w: a %s record of task %s attributed to %q is no Sensei observation", ErrMalformedEvent, e.Kind, e.TaskID, e.Source)
		}
		if !m.has("evidence") {
			if operation != "" {
				return fmt.Errorf("%w: a %s record of task %s carries no evidence envelope", ErrMalformedEvent, e.Kind, e.TaskID)
			}
			return nil
		}
		if !m.object("evidence") {
			return fmt.Errorf("%w: a %s record of task %s carries an evidence envelope that is not one", ErrMalformedEvent, e.Kind, e.TaskID)
		}
		env := m.member("evidence")
		if err := evidenceEnvelopeShape.matches(env); err != nil {
			return fmt.Errorf("%w: the evidence envelope of a %s record of task %s: %v", ErrMalformedEvent, e.Kind, e.TaskID, err)
		}
		switch op, _ := env.str("operation"); {
		case operation != "" && op != operation:
			return fmt.Errorf("%w: a %s record of task %s records operation %q, not %s", ErrMalformedEvent, e.Kind, e.TaskID, op, operation)
		case op != operationPreflight && op != operationAuditDiff:
			return fmt.Errorf("%w: a %s record of task %s records operation %q, which is no Sensei operation it records", ErrMalformedEvent, e.Kind, e.TaskID, op)
		}
		return nil
	}
}

// transportGitHub is the one transport a GitHub-carried role turn names.
const transportGitHub = "github"

// transportSemantics: a role turn that names its transport names GitHub's.
func transportSemantics(e event.Event, m payloadMembers) error {
	if t, ok := m.str("transport"); m.has("transport") && (!ok || t != transportGitHub) {
		return fmt.Errorf("%w: a %s record of task %s names transport %s", ErrMalformedEvent, e.Kind, e.TaskID, m["transport"])
	}
	return nil
}

// agentOutcomes is the closed vocabulary of how a GitHub-carried turn ended
// without an answer.
var agentOutcomes = map[string]bool{"refused": true, "malformed_answer": true, "unanswered": true}

func agentOutcomeSemantics(e event.Event, m payloadMembers) error {
	if o, ok := m.str("outcome"); m.has("outcome") && (!ok || !agentOutcomes[o]) {
		return fmt.Errorf("%w: a %s record of task %s ends as %s, which is no outcome", ErrMalformedEvent, e.Kind, e.TaskID, m["outcome"])
	}
	return nil
}

// statusReviewKinds is the closed vocabulary of a progress report about a
// review that did not happen independently.
var statusReviewKinds = map[string]bool{"advisory": true, "human_override": true, "lifecycle_fault": true}

// statusSemantics: a progress report about a review names a review kind of
// the closed vocabulary and records that no independent review happened; a
// report naming a session names its own; a decision of a review verdict is
// one of the closed vocabulary.
func statusSemantics(e event.Event, m payloadMembers) error {
	if m.has("review_kind") {
		if k, _ := m.str("review_kind"); !statusReviewKinds[k] {
			return fmt.Errorf("%w: a status of task %s reports review kind %s", ErrMalformedEvent, e.TaskID, m["review_kind"])
		}
		if independent, ok := m.boolean("independent_review"); !ok || independent {
			return fmt.Errorf("%w: a status of task %s about a review does not record that no independent review happened", ErrMalformedEvent, e.TaskID)
		}
	}
	if session, ok := m.str("session_id"); m.has("session_id") && (!ok || session != e.SessionID) {
		return fmt.Errorf("%w: a status recorded by session %s of task %s names session %s", ErrMalformedEvent, e.SessionID, e.TaskID, m["session_id"])
	}
	if m.has("TaskID") {
		if err := ownTask(e, m, "TaskID"); err != nil {
			return err
		}
	}
	return nil
}

// reviewDecisionSemantics: a review's decision is one of the closed
// vocabulary.
func reviewDecisionSemantics(e event.Event, m payloadMembers) error {
	if d, _ := m.str("decision"); !validReviewDecision(d) {
		return fmt.Errorf("%w: a %s record of task %s decides %s", ErrMalformedEvent, e.Kind, e.TaskID, m["decision"])
	}
	return nil
}

// reconciliationSemantics: a reconciliation rests on an authority of the
// closed vocabulary.
func reconciliationSemantics(e event.Event, m payloadMembers) error {
	if a, _ := m.str("authority"); !validReconciliationAuthority(a) {
		return fmt.Errorf("%w: a reconciliation of task %s rests on authority %s", ErrMalformedEvent, e.TaskID, m["authority"])
	}
	return nil
}

// inspectSemantics: an inspection's completion names the inspect mode.
func inspectSemantics(e event.Event, m payloadMembers) error {
	if !m.has("mode") {
		return nil
	}
	if mode, _ := m.str("mode"); mode != "inspect" {
		return fmt.Errorf("%w: a completion of task %s names mode %s", ErrMalformedEvent, e.TaskID, m["mode"])
	}
	return nil
}

// planAdmissionRefusalIfOne applies the plan-admission refusal's own test to
// a completion that records one.
func planAdmissionRefusalIfOne(e event.Event, m payloadMembers, present bool) error {
	if planAdmissionRefusalShape.matches(m) != nil {
		return nil
	}
	return planAdmissionRefusalPayload(e, m, present)
}

// checkpointStatusSemantics: a failure that names a checkpoint status names
// one of the closed vocabulary, and one about a task names its own.
func checkpointStatusSemantics(e event.Event, m payloadMembers) error {
	if st, ok := m.str("checkpoint_status"); m.has("checkpoint_status") && (!ok || !CheckpointStatus(st).Valid()) {
		return fmt.Errorf("%w: a failure of task %s names checkpoint status %s", ErrMalformedEvent, e.TaskID, m["checkpoint_status"])
	}
	return nil
}

// statusShapes are the progress reports the workflow and its transports
// record, each as its one emitter marshals it.
var statusShapes = []shape{
	{name: "a review-acceptance fault", members: map[string]member{
		"request_id": req(tString), "review_digest": req(tString), "error": req(tString), "transport": req(tText)}},
	{name: "a supersession that could not close", members: map[string]member{
		"superseded_request": req(tString), "request_id": req(tString), "reason": req(tString), "withdrawn": req(tBool),
		"closed": req(tBool), "close_error": req(tString), "transport": req(tText)}},
	{name: "a review supersession", members: map[string]member{
		"superseded_request": req(tString), "request_id": req(tString), "reason": req(tString),
		"recorded_candidate_digest": req(tString), "candidate_digest": req(tString), "recorded_candidate_tree": req(tString),
		"candidate_tree": req(tString), "superseded_provider": req(tString), "reviewer_provider": req(tString),
		"withdrawn": req(tBool), "closed": req(tBool), "transport": req(tText), "withdraw_error": opt(tString)}},
	reconciliationShape,
	{name: "an advisory review fault", members: map[string]member{
		"review_kind": req(tText), "independent_review": req(tBool), "error": req(tString)}},
	{name: "an advisory attestation fault", members: map[string]member{
		"review_kind": req(tText), "independent_review": req(tBool), "attested_review_digest": req(tString),
		"review_digest": req(tString), "error": req(tString)}},
	{name: "a human override", members: map[string]member{
		"review_kind": req(tText), "independent_review": req(tBool), "adversarial_obligation_unmet": req(tBool),
		"attesting_principal": req(tString), "reviewer_provider": req(tString), "request_id": req(tString),
		"review_digest": req(tString), "candidate_digest": req(tString), "candidate_tree": req(tString),
		"base": req(tString), "statement": req(tString)}},
	{name: "a gap routing", members: map[string]member{
		"Route": req(tString), "Condition": req(tString), "Gap": req(tObjectOr), "ClaimGap": req(tString),
		"Basis": req(tNumber), "Closes": req(tString), "Blast": req(tString), "Gate": req(tString)}},
	{name: "a failed publication", members: map[string]member{"pushed": req(tBool), "candidate": req(tString)}},
	{name: "an incomplete implementer attempt", members: map[string]member{"implementer_incomplete": req(tObject)}},
	{name: "an advisory review", members: map[string]member{"review_kind": req(tText), "independent_review": req(tBool)}},
	{name: "a finding account", members: map[string]member{"classification_disputes": req(tArray), "open_findings": req(tArray)}},
	{name: "the required-test runs", members: map[string]member{"required_test_runs": req(tArray)}},
	{name: "an advisory review obligation", members: map[string]member{
		"review_kind": req(tText), "independent_review": req(tBool), "candidate": req(tString), "obligation_reason": req(tString)}},
	{name: "an architect's adjudication", members: architectureDecisionMembers(nil)},
	{name: "a review cycle", members: map[string]member{"cycle": req(tNumber)}},
	{name: "an error", members: map[string]member{"error": req(tString)}},
	{name: "a gap receipt", members: map[string]member{"gap": req(tString), "gap_identity": req(tObjectOr)}},
	{name: "a gap", members: map[string]member{"gap_identity": req(tObjectOr)}},
	{name: "an advisory verdict", members: map[string]member{
		"review_kind": req(tText), "independent_review": req(tBool), "decision": req(tString), "provider": req(tString),
		"session_mode": req(tString), "candidate": req(tString), "adversarial_obligation_unmet": req(tBool),
		"obligation_reason": req(tString)}},
	{name: "contradicted premises", members: map[string]member{"plan_attempt_id": req(tString), "contradicted_premises": req(tArray)}},
	{name: "a route account", members: map[string]member{
		"derived_coverage_anchors": req(tNumber), "planned_files": req(tNumber), "route": req(tString), "closes_gap": req(tBool),
		"anchors": req(tArray), "operational_authority": req(tArray), "unexamined": req(tArray)}},
	authorityDecisionShape,
	{name: "a candidate identity", members: map[string]member{
		"task_id": req(tString), "repository": req(tString), "domain": req(tString), "base_sha": req(tString),
		"worktree_state": req(tString), "worktree": req(tString), "branch": req(tString), "created_at": req(tText),
		"graph_build_commit": opt(tString), "source_repo_commit": opt(tString), "resolution": opt(tObjectOr)}},
	{name: "a stop by the caller", members: map[string]member{"handoff": req(tBool), "stopped_by_caller": req(tBool)}},
	{name: "a review lifecycle fault", members: map[string]member{
		"review_kind": req(tText), "independent_review": req(tBool), "handoff": req(tBool), "error": req(tString)}},
	{name: "an unavailable provider", members: map[string]member{"handoff": req(tBool), "provider_unavailable": req(tBool)}},
	planAdmissionRefusalShape,
	blockedExternalShape,
	{name: "a waiting-review reconciliation", members: map[string]member{
		"recorded_request": req(tString), "recorded_base": req(tString), "recorded_candidate_digest": req(tString),
		"recorded_candidate_tree": req(tString), "base": req(tString), "candidate_digest": req(tString),
		"candidate_tree": req(tString), "same_candidate": req(tBool)}},
}

// agentStartedShapes and agentFinishedShapes are the role-turn reports of the
// control runner and of the GitHub-carried architect and reviewer turns.
var (
	agentStartedShapes = []shape{
		{name: "a control turn start", members: map[string]member{
			"turn_id": req(tText), "role": req(tText), "principal": req(tString), "role_session": req(tString), "expires_at": req(tText)}},
		{name: "a GitHub request fault", members: map[string]member{
			"request_id": req(tString), "request_comment": req(tNumber), "error": req(tString), "transport": req(tText)}},
		{name: "a GitHub request", members: map[string]member{"request_id": req(tString), "transport": req(tText)}},
		{name: "a bound architecture request", members: map[string]member{
			"request_id": req(tString), "objective_digest": req(tString), "base": req(tString), "graph_repository": req(tString),
			"graph_build_commit": req(tString), "transport": req(tText)}},
		{name: "a bound review request", members: map[string]member{
			"request_id": req(tString), "request_comment": req(tNumber), "base": req(tString), "candidate_digest": req(tString),
			"candidate_tree": req(tString), "review_commit": req(tString), "reviewer_provider": req(tString),
			"reattached": req(tBool), "waiter_deadline": req(tText), "transport": req(tText)}},
	}
	architectureRequestMembers = func(extra map[string]member) map[string]member {
		out := map[string]member{"request_id": req(tString), "request_comment": req(tNumber), "objective_digest": req(tString),
			"base": req(tString), "graph_repository": req(tString), "graph_build_commit": req(tString), "transport": req(tText)}
		for k, m := range extra {
			out[k] = m
		}
		return out
	}
	agentFinishedShapes = []shape{
		{name: "a control turn end", members: map[string]member{"turn_id": req(tText)}},
		{name: "a refused architecture answer", members: architectureRequestMembers(map[string]member{
			"outcome": req(tText), "refusal_stage": req(tText), "refusal_reason": req(tString), "refusal_comment": req(tNumber),
			"github_author": req(tString), "github_author_id": req(tNumber), "reason": req(tString), "exchange_closed": req(tBool),
			"exchange_close_error": opt(tString)})},
		{name: "a malformed architecture answer", members: architectureRequestMembers(map[string]member{
			"outcome": req(tText), "malformed_comment": req(tNumber), "github_author": req(tString), "github_author_id": req(tNumber),
			"reason": req(tString), "exchange_closed": req(tBool), "exchange_close_error": opt(tString)})},
		{name: "an unanswered architecture request", members: architectureRequestMembers(map[string]member{
			"waited": req(tString), "outcome": req(tText), "request_state": req(tString), "reason": req(tString)})},
		{name: "an architecture answer", members: map[string]member{
			"request_id": req(tString), "objective_digest": req(tString), "base": req(tString), "graph_repository": req(tString),
			"graph_build_commit": req(tString), "github_author": req(tString), "github_author_id": req(tNumber), "transport": req(tText)}},
		{name: "an unanswered review request", members: map[string]member{
			"request_id": req(tString), "candidate_digest": req(tString), "candidate_tree": req(tString), "base": req(tString),
			"review_commit": req(tString), "waited": req(tString), "outcome": req(tText), "reason": req(tString), "transport": req(tText)}},
		{name: "a review answer", members: map[string]member{
			"request_id": req(tString), "candidate_tree": req(tString), "review_commit": req(tString), "github_author": req(tString),
			"github_author_id": req(tNumber), "transport": req(tText)}},
		{name: "a staged review", members: map[string]member{
			"request_id": req(tString), "reviewer_provider": req(tString), "review_digest": req(tString), "standing": req(tString),
			"transport_evidence": req(tArray), "staged_evidence": req(tArray), "base": req(tString), "candidate_digest": req(tString),
			"candidate_tree": req(tString), "review_commit": req(tString), "transport": req(tText)}},
	}
	outputShape         = shape{name: "a role's output", members: map[string]member{"stream": req(tText)}}
	changeReportedShape = shape{name: "a change report", members: map[string]member{
		"files": req(tArray), "awareness": opt(tObjectOr), "audit": opt(tString), "risk": opt(tString), "governing": opt(tArray)}}
	contextConsultedShape = shape{name: "a consulted context", members: map[string]member{"sources": req(tArray)}}
	handoffShape          = shape{name: "a worker handoff", members: map[string]member{
		"provenance": req(tObject), "state": req(tText), "previous_worker": opt(tString), "open_findings": opt(tArray),
		"cycles_used": opt(tNumber), "cycles_allowed": opt(tNumber)}}
	contradictionShape = shape{name: "a review contradiction", members: map[string]member{
		"reviewer": req(tString), "review_attempt": req(tNumber), "decision": req(tText), "summary": req(tString),
		"candidate_digest": req(tString), "evidence_identity": req(tString), "findings": opt(tArray), "candidate_tree": opt(tString)}}
	reconciliationShape = shape{name: "a reconciliation", members: map[string]member{
		"provenance": req(tObject), "disputed": req(tString), "inputs": req(tArray), "canonical": req(tArray),
		"decision": req(tString), "authority": req(tText), "remaining": opt(tString)}}
	routineShape = shape{name: "a routine classification", members: map[string]member{
		"routine": req(tBool), "qualifying": opt(tStrings), "blocking": opt(tString)}}
	validationShape = shape{name: "a validation bundle", members: map[string]member{
		"candidate_id": req(tString), "diff_digest": req(tString), "checks": req(tArray)}}
	inspectCompletedShape = shape{name: "a completed inspection", members: map[string]member{
		"implementor": req(tString), "plan": req(tString), "mode": req(tText)}}
	runSettledShape = shape{name: "a settled run", members: map[string]member{
		"workspace": req(tString), "implementor": req(tString), "plan": req(tString), "review": req(tString),
		"audit": req(tString), "publication": req(tString)}}
	implementerIncompleteShape = shape{name: "an incomplete implementer", members: map[string]member{
		"state": req(tText), "task_id": req(tText), "plan_attempt_id": req(tString), "review_cycle": req(tNumber),
		"review_attempt": req(tNumber), "attempts": req(tNumber), "max_attempts": req(tNumber), "owed_findings": req(tArray),
		"retained_findings": opt(tArray), "open_operations": opt(tArray), "diagnosis": opt(tString)}}
	obligationPersistenceFailedShape = shape{name: "an unpersisted obligation", members: map[string]member{
		"state": req(tText), "task_id": req(tText), "plan_attempt_id": req(tString), "review_cycle": req(tNumber),
		"checkpoint_status": req(tText), "attempts": req(tNumber), "prior_checkpoint": req(tString),
		"prior_checkpoint_id": opt(tString), "cause": req(tString)}}
)

// The closed shapes of the kinds above, as their emitters marshal them: a
// member every emitter records is required, an omitempty one optional.
var (
	lineageTransitionShape = shape{name: "a session-lineage transition", members: map[string]member{
		"task_id": req(tText), "holder_session_id": req(tText), "parent_session_id": req(tText),
		"current_session_id": req(tText), "relation": req(tString)}}
	// planProposedShape is the operative plan transition
	// (workflow.proposedPlan): the decision, and the plan source, digest,
	// answering architect and plan attempt the workflow binds to it.
	planProposedShape = shape{name: "a proposed plan", members: architectureDecisionMembers(map[string]member{
		"plan_source": opt(tText), "plan_digest": opt(tString), "architect": opt(tString), "plan_attempt_id": opt(tText)})}
	modeSelectedShape = shape{name: "a mode selection", members: map[string]member{
		"mode": req(tText), "provenance": req(tText)}}
	// architectSpokeShape is the architect's decision as it answered
	// (workflow.architectureDecision); an assisted answer records none.
	architectSpokeShape    = shape{name: "an architect's decision", members: architectureDecisionMembers(nil)}
	pullRequestShape       = shape{name: "an opened pull request", members: map[string]member{"url": req(tText)}}
	architectAssignedShape = shape{name: "an architect assignment", members: map[string]member{
		"role": req(tText), "provider": req(tText), "roster_position": req(tNumber), "roster_size": req(tNumber)}}
	reviewerAssignedShape = shape{name: "a reviewer assignment", members: map[string]member{
		"role": req(tText), "provider": req(tText), "session": req(tText), "excluded": req(tString),
		"candidate": req(tString), "review_attempt": req(tNumber)}}
	candidateChangedShape = shape{name: "a candidate change", members: map[string]member{
		"cycle": req(tNumber), "review_attempt": req(tNumber)}}
	artifactExcludedShape = shape{name: "an excluded candidate artifact", members: map[string]member{
		"path": req(tText), "class": req(tString), "size": req(tString), "reason": req(tString)}}
	reviewStartedShape = shape{name: "a review start", members: map[string]member{
		"candidate": req(tText), "review_attempt": opt(tNumber)}}
	reviewFindingShape = shape{name: "a review finding", members: map[string]member{
		"id": req(tText), "severity": req(tText), "class": opt(tString), "claim": req(tText), "reference": opt(tString),
		"reason": req(tString), "correction": opt(tString), "proof_gap": opt(tString)}}
	reviewCompletedShape = shape{name: "a review verdict", members: map[string]member{
		"provenance": req(tObject), "decision": req(tText), "summary": req(tString), "instructions": opt(tString),
		"findings": opt(tArray)}}
	receiptShape = shape{name: "a run receipt record", members: map[string]member{
		"receipt": req(tObject), "completeness": req(tText), "missing": req(tStrings), "candidate_state_disagreement": opt(tString)}}
	decisionShape = shape{name: "an authority decision", members: map[string]member{
		"level": req(tNumber), "subject": req(tText), "reason": req(tString), "recommendation": opt(tString), "options": opt(tArray)}}
	authorityAnswerShape = shape{name: "a confirmation answer", members: map[string]member{
		"option": req(tText)}}
	// authorityDecisionShape is an authority.Resolution as it marshals, and
	// authorityResolutionShape the AuthorityResolved answer about a condition
	// that carries it with the gap it settles and the plan attempt it was
	// asked about.
	authorityDecisionShape   = shape{name: "an authority resolution", members: authorityResolutionMembers(nil)}
	authorityResolutionShape = shape{name: "an answer about a condition", members: authorityResolutionMembers(map[string]member{
		"gap_identity": opt(tObject), "plan_attempt_id": opt(tText)})}
	planAttemptStartedShape = shape{name: "a plan attempt", members: map[string]member{
		"plan_attempt_id": req(tText), "task_id": req(tText), "world": req(tString), "plan_source": req(tText),
		"plan_digest": opt(tString), "plan": req(tObject)}}
	planAdmissionRefusalShape = shape{name: "a plan-admission refusal", members: map[string]member{
		"plan_attempt_id": req(tText), "task_id": req(tText), "reason": req(tText), "refusal_class": opt(tText),
		"declaration": opt(tStructured), "governing_evidence_id": opt(tText), "refusal_id": opt(tText), "continuation": opt(tText)}}
	grantShape = shape{name: "a grant record", members: map[string]member{
		"world": req(tString), "grants": req(tArray), "plan_attempt_id": opt(tText)}}
	checkpointShape = shape{name: "a checkpoint binding", members: map[string]member{
		"checkpoint_id": req(tText), "previous_checkpoint_id": opt(tText), "task_id": req(tText), "plan_attempt_id": req(tText),
		"status": req(tText), "retirement": opt(tText), "payload_digest": req(tText), "replay_digest": req(tText)}}
	awaitingAuthorityShape = shape{name: "a standing question", members: map[string]member{
		"condition": req(tString), "domain": req(tString), "base_sha": req(tString), "decision": req(tObject),
		"task_id": req(tText), "session_id": req(tText), "scope": opt(tStrings), "scope_recorded": req(tBool),
		"gap_identity": opt(tObject), "plan_attempt_id": opt(tText)}}
	awaitingReviewShape = shape{name: "an owed review", members: map[string]member{
		"review_kind": req(tText), "independent_review": req(tBool), "obligations": req(tStrings),
		"request_id": opt(tString), "request_comment": opt(tNumber), "conversation": opt(tString), "base": opt(tString),
		"candidate_digest": opt(tString), "candidate_tree": opt(tString), "review_commit": opt(tString),
		"waited": opt(tString), "review_attempt": opt(tNumber), "reviewers_tried": opt(tStrings), "handoff": opt(tBool),
		"error": opt(tString), "observed": opt(tArray), "observations": opt(tArray)}}
	blockedExternalShape = shape{name: "an external block", members: map[string]member{
		"task_id": req(tText), "role": req(tText), "provider": req(tString), "reason": req(tText),
		"retry_at_state": req(tText), "retry_at": opt(tText), "detail": opt(tText)}}
	notConvergedShape = shape{name: "an owed re-plan", members: map[string]member{
		"task_id": req(tText), "implementers": req(tStrings), "review_cycles": req(tNumber), "owed": req(tText)}}
	restorationRefusedShape = shape{name: "a refused restoration", members: map[string]member{
		"task_id": req(tText), "subject": req(tText), "instrument": req(tText), "binding": req(tText),
		"detail": req(tText), "measured": opt(tObject)}}
	baseMovedShape = shape{name: "a base-moved refusal", members: map[string]member{
		"TaskID": req(tText), "Recorded": req(tText), "Current": req(tText)}}
	dirtyCanonicalShape = shape{name: "a dirty-canonical refusal", members: map[string]member{
		"Repository": req(tText)}}
)

// THE CLOSED PAYLOAD SHAPES (RULING-200 A). No governed kind accepts an
// arbitrary payload: each names, in governedKinds, either noPayload -- the
// envelope and summary are the whole record -- or a validator that admits
// exactly the explicitly enumerated shapes its emitters record. A shape
// declares every member it may carry and the JSON type of each, and which of
// them it requires; a payload matches it only when every member it carries is
// declared, of its declared type, and every required member is present. So an
// undeclared member, a member of the wrong type and a missing required member
// each fail closed, whatever the kind.

// memberType is the JSON type a declared payload member must have.
type memberType int

const (
	tString     memberType = iota // a JSON string, possibly empty
	tText                         // a non-empty JSON string
	tNumber                       // a JSON number
	tBool                         // a JSON boolean
	tObject                       // a JSON object
	tObjectOr                     // a JSON object, or null
	tArray                        // a JSON array, or null (an empty Go slice marshals as null)
	tStrings                      // a JSON array of strings, or null
	tStructured                   // a JSON object, a JSON array, or null
)

// member is one declared member of a shape: its type, and whether the shape
// requires it.
type member struct {
	t        memberType
	required bool
}

func req(t memberType) member { return member{t: t, required: true} }
func opt(t memberType) member { return member{t: t} }

// shape is one closed payload variant, named for the error that refuses it.
type shape struct {
	name    string
	members map[string]member
}

// matches reports why m is not exactly s, or nil when it is.
func (s shape) matches(m payloadMembers) error {
	for k := range m {
		decl, ok := s.members[k]
		if !ok {
			return fmt.Errorf("it carries %q, which %s does not declare", k, s.name)
		}
		if !hasType(m, k, decl.t) {
			return fmt.Errorf("its %q is not of the type %s declares", k, s.name)
		}
	}
	for k, decl := range s.members {
		if decl.required && !m.has(k) {
			return fmt.Errorf("it carries no %q, which %s requires", k, s.name)
		}
	}
	return nil
}

// hasType reports whether member key of m is of type t.
func hasType(m payloadMembers, key string, t memberType) bool {
	raw := strings.TrimSpace(string(m[key]))
	if raw == "" {
		return false
	}
	switch t {
	case tString:
		_, ok := m.str(key)
		return ok
	case tText:
		v, ok := m.str(key)
		return ok && strings.TrimSpace(v) != ""
	case tNumber:
		_, ok := m.number(key)
		return ok
	case tBool:
		_, ok := m.boolean(key)
		return ok
	case tObject:
		return raw[0] == '{'
	case tObjectOr:
		return raw[0] == '{' || raw == "null"
	case tArray:
		return raw[0] == '[' || raw == "null"
	case tStructured:
		return raw[0] == '{' || raw[0] == '[' || raw == "null"
	case tStrings:
		if raw == "null" {
			return true
		}
		_, ok := m.strs(key)
		return ok
	}
	return false
}

// exactlyOneOf refuses a payload that is absent or that is none of shapes.
func exactlyOneOf(e event.Event, m payloadMembers, present bool, shapes ...shape) error {
	if err := requiredObject(e, m, present); err != nil {
		return err
	}
	var why []string
	for _, s := range shapes {
		err := s.matches(m)
		if err == nil {
			return nil
		}
		why = append(why, err.Error())
	}
	return fmt.Errorf("%w: a %s record of task %s is no shape its kind records: %s",
		ErrMalformedEvent, e.Kind, e.TaskID, strings.Join(why, "; "))
}

// noPayload is the payload of a kind whose envelope and summary are the whole
// record: none at all.
func noPayload(e event.Event, _ payloadMembers, present bool) error {
	if present {
		return fmt.Errorf("%w: a %s record of task %s carries a payload, which its kind does not", ErrMalformedEvent, e.Kind, e.TaskID)
	}
	return nil
}

// requiredObject is a payload that must be present as one JSON object.
func requiredObject(e event.Event, _ payloadMembers, present bool) error {
	if !present {
		return fmt.Errorf("%w: a %s event of task %s carries no payload", ErrMalformedEvent, e.Kind, e.TaskID)
	}
	return nil
}

// requireMembers is requiredObject whose named members are each present as
// a non-empty string.
func requireMembers(e event.Event, m payloadMembers, present bool, keys ...string) error {
	if err := requiredObject(e, m, present); err != nil {
		return err
	}
	for _, k := range keys {
		if v, ok := m.str(k); !ok || strings.TrimSpace(v) == "" {
			return fmt.Errorf("%w: a %s event of task %s names no %s", ErrMalformedEvent, e.Kind, e.TaskID, k)
		}
	}
	return nil
}

// optionalString refuses a member that is present and not a non-empty string.
func optionalString(e event.Event, m payloadMembers, key string) error {
	if !m.has(key) {
		return nil
	}
	if v, ok := m.str(key); !ok || strings.TrimSpace(v) == "" {
		return fmt.Errorf("%w: a %s event of task %s states a %s that is not one", ErrMalformedEvent, e.Kind, e.TaskID, key)
	}
	return nil
}

func lineageTransitionPayload(e event.Event, _ payloadMembers, _ bool) error {
	var b SessionLineageBinding
	if err := DecodeExactlyOne(e.Payload, &b); err != nil {
		return fmt.Errorf("%w: a session-lineage transition of task %s does not decode: %v", ErrMalformedEvent, e.TaskID, err)
	}
	if b.TaskID != e.TaskID || b.CurrentSessionID != e.SessionID {
		return fmt.Errorf("%w: a session-lineage transition recorded as task %s by session %s binds task %q to session %q",
			ErrMalformedEvent, e.TaskID, e.SessionID, b.TaskID, b.CurrentSessionID)
	}
	return nil
}

// planProposedPayload: the operative plan transition carries its decision
// and its plan, names its plan attempt when it states one, states a plan
// source of the closed vocabulary when it states one, and is the system's
// exactly when the plan was supplied rather than the architect's.
func planProposedPayload(e event.Event, m payloadMembers, present bool) error {
	if err := requireMembers(e, m, present, "decision"); err != nil {
		return err
	}
	if _, ok := m.str("plan"); !ok {
		return fmt.Errorf("%w: a plan of task %s carries no plan", ErrMalformedEvent, e.TaskID)
	}
	if err := optionalString(e, m, "plan_attempt_id"); err != nil {
		return err
	}
	source, ok := m.str("plan_source")
	if m.has("plan_source") && (!ok || !planSources[source]) {
		return fmt.Errorf("%w: a plan of task %s states plan source %s, which is no plan source", ErrMalformedEvent, e.TaskID, m["plan_source"])
	}
	if supplied := source == planSourceSupplied; supplied != (e.Source == event.SourceSystem) {
		return fmt.Errorf("%w: a plan of task %s from plan source %q is attributed to %q", ErrMalformedEvent, e.TaskID, source, e.Source)
	}
	return nil
}

// planSourceSupplied is the plan source of an operator-supplied plan, the one
// plan the system rather than the architect records; planSources is the
// closed plan-source vocabulary, read by membership.
const planSourceSupplied = "supplied"

var planSources = map[string]bool{"architect": true, planSourceSupplied: true}

func planAttemptStartedPayload(e event.Event, m payloadMembers, present bool) error {
	if err := requireMembers(e, m, present, "plan_attempt_id", "task_id", "plan_source"); err != nil {
		return err
	}
	if _, ok := m.str("world"); !ok || !m.object("plan") {
		return fmt.Errorf("%w: plan attempt of task %s states no world or no plan", ErrMalformedEvent, e.TaskID)
	}
	return nil
}

// THE STANDING-STATE PAYLOADS (70B2a1 cycle-3 review f2). Each kind below
// carries an obligation a later session restores -- a standing question, a
// provider block, an owed re-plan, an owed review, a refused restoration or
// precondition, an owed plan-admission turn -- so each is validated as its
// complete durable shape: present, every required member present and
// non-empty, closed vocabularies read by membership, the envelope's task (and
// session, where the record states one) its own, and no member the shape does
// not declare. An absent or partial record of one of them is refused at the
// Store, so no record can replace a standing obligation with nothing.

// exactMembers refuses a payload that is absent, or that names a member
// outside allowed.
func exactMembers(e event.Event, m payloadMembers, present bool, allowed ...string) error {
	if err := requiredObject(e, m, present); err != nil {
		return err
	}
	declared := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		declared[k] = true
	}
	for k := range m {
		if !declared[k] {
			return fmt.Errorf("%w: a %s record of task %s carries %q, which its shape does not declare", ErrMalformedEvent, e.Kind, e.TaskID, k)
		}
	}
	return nil
}

// ownTask requires member key to name the envelope's task.
func ownTask(e event.Event, m payloadMembers, key string) error {
	if task, ok := m.str(key); !ok || task != e.TaskID {
		return fmt.Errorf("%w: a %s record in task %s's record is about task %s", ErrMalformedEvent, e.Kind, e.TaskID, m[key])
	}
	return nil
}

// stringMember requires member key to be present as a string, empty or not.
func stringMember(e event.Event, m payloadMembers, key string) error {
	if _, ok := m.str(key); !ok {
		return fmt.Errorf("%w: a %s record of task %s states no %s", ErrMalformedEvent, e.Kind, e.TaskID, key)
	}
	return nil
}

// nonEmptyStrings requires member key to be an array of non-empty strings,
// with at least one when required.
func nonEmptyStrings(e event.Event, m payloadMembers, key string, required bool) error {
	vs, ok := m.strs(key)
	if !ok || (required && len(vs) == 0) {
		return fmt.Errorf("%w: a %s record of task %s states no %s", ErrMalformedEvent, e.Kind, e.TaskID, key)
	}
	for _, v := range vs {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("%w: a %s record of task %s states an empty %s", ErrMalformedEvent, e.Kind, e.TaskID, key)
		}
	}
	return nil
}

// planAdmissionRefusalPayload is a plan-admission refusal, recorded on the
// attempt (PlanAttemptRefused) or as the terminal an invocation parked on
// (WorkflowPlanAdmissionRefused): the attempt it refused, its own task, the
// reason, and -- when it states one -- the one continuation that owes an
// architect turn.
func planAdmissionRefusalPayload(e event.Event, m payloadMembers, present bool) error {
	if err := exactMembers(e, m, present, "plan_attempt_id", "task_id", "reason", "refusal_class", "declaration",
		"governing_evidence_id", "refusal_id", "continuation"); err != nil {
		return err
	}
	if err := requireMembers(e, m, present, "plan_attempt_id", "reason"); err != nil {
		return err
	}
	if err := ownTask(e, m, "task_id"); err != nil {
		return err
	}
	for _, k := range []string{"refusal_class", "governing_evidence_id", "refusal_id"} {
		if err := optionalString(e, m, k); err != nil {
			return err
		}
	}
	if c, ok := m.str("continuation"); m.has("continuation") && (!ok || c != PlanAdmissionContinuationArchitectTurn) {
		return fmt.Errorf("%w: a %s record of task %s continues as %s, which is no continuation", ErrMalformedEvent, e.Kind, e.TaskID, m["continuation"])
	}
	return nil
}

// decisionMembers is an authority decision: a level of the closed
// vocabulary, its subject and reason, and options each naming itself.
func decisionMembers(e event.Event, d payloadMembers) error {
	for k := range d {
		switch k {
		case "level", "subject", "reason", "recommendation", "options":
		default:
			return fmt.Errorf("%w: a %s decision of task %s carries %q, which a decision does not declare", ErrMalformedEvent, e.Kind, e.TaskID, k)
		}
	}
	if level, ok := d.integer("level"); !ok || !validAuthorityLevel(level) {
		return fmt.Errorf("%w: a %s decision of task %s states level %s, which is no authority level", ErrMalformedEvent, e.Kind, e.TaskID, d["level"])
	}
	if subject, ok := d.str("subject"); !ok || strings.TrimSpace(subject) == "" {
		return fmt.Errorf("%w: a %s decision of task %s names no subject", ErrMalformedEvent, e.Kind, e.TaskID)
	}
	if _, ok := d.str("reason"); !ok {
		return fmt.Errorf("%w: a %s decision of task %s states no reason", ErrMalformedEvent, e.Kind, e.TaskID)
	}
	if d.has("options") {
		options, ok := d.objects("options")
		if !ok {
			return fmt.Errorf("%w: a %s decision of task %s states options that are not options", ErrMalformedEvent, e.Kind, e.TaskID)
		}
		for _, o := range options {
			if id, ok := o.str("id"); !ok || strings.TrimSpace(id) == "" {
				return fmt.Errorf("%w: a %s decision of task %s offers an option naming no id", ErrMalformedEvent, e.Kind, e.TaskID)
			}
			if outcome, ok := o.str("outcome"); o.has("outcome") && (!ok || (outcome != "" && !validAuthorityOutcome(outcome))) {
				return fmt.Errorf("%w: a %s decision of task %s offers an option with outcome %s", ErrMalformedEvent, e.Kind, e.TaskID, o["outcome"])
			}
		}
	}
	return nil
}

// authorityRequiredPayload is the decision a person is asked for.
func authorityRequiredPayload(e event.Event, m payloadMembers, present bool) error {
	if err := requiredObject(e, m, present); err != nil {
		return err
	}
	return decisionMembers(e, m)
}

// awaitingAuthorityPayload is a standing question: the decision asked, where
// it was asked -- this task and this session -- the condition, domain and
// base it was asked under, and whether its scope was recorded.
func awaitingAuthorityPayload(e event.Event, m payloadMembers, present bool) error {
	if err := exactMembers(e, m, present, "condition", "domain", "base_sha", "decision", "task_id", "session_id",
		"scope", "scope_recorded", "gap_identity", "plan_attempt_id"); err != nil {
		return err
	}
	if err := ownTask(e, m, "task_id"); err != nil {
		return err
	}
	if session, ok := m.str("session_id"); !ok || session != e.SessionID {
		return fmt.Errorf("%w: a standing question recorded by session %s of task %s was asked in session %s",
			ErrMalformedEvent, e.SessionID, e.TaskID, m["session_id"])
	}
	for _, k := range []string{"condition", "domain", "base_sha"} {
		if err := stringMember(e, m, k); err != nil {
			return err
		}
	}
	if _, ok := m.boolean("scope_recorded"); !ok {
		return fmt.Errorf("%w: a standing question of task %s does not say whether its scope was recorded", ErrMalformedEvent, e.TaskID)
	}
	if m.has("scope") {
		if err := nonEmptyStrings(e, m, "scope", false); err != nil {
			return err
		}
	}
	if m.has("gap_identity") && !m.object("gap_identity") {
		return fmt.Errorf("%w: a standing question of task %s states a gap identity that is not one", ErrMalformedEvent, e.TaskID)
	}
	if err := optionalString(e, m, "plan_attempt_id"); err != nil {
		return err
	}
	if !m.object("decision") {
		return fmt.Errorf("%w: a standing question of task %s records no decision", ErrMalformedEvent, e.TaskID)
	}
	return decisionMembers(e, m.member("decision"))
}

// The external-block retry states, and the one reason that names no provider:
// an exhausted architect roster whose last entry the configuration left
// unnamed. They are the workflow's ExternalBlock vocabulary
// (workflow.RetryAtKnown, workflow.RetryAtUnknown,
// workflow.ReasonNoArchitectObtained), read here by membership.
const (
	retryAtKnown        = "KNOWN"
	retryAtUnknown      = "UNKNOWN"
	noArchitectObtained = "ENGINE_NO_ARCHITECT_OBTAINED"
)

// blockedExternalPayload is the role turn a provider proved it cannot serve
// now: its own task, a role of the closed vocabulary, the provider and its
// structured reason, and a retry time stated exactly when it is KNOWN.
func blockedExternalPayload(e event.Event, m payloadMembers, present bool) error {
	if err := exactMembers(e, m, present, "task_id", "role", "provider", "reason", "retry_at_state", "retry_at", "detail"); err != nil {
		return err
	}
	if err := ownTask(e, m, "task_id"); err != nil {
		return err
	}
	role, _ := m.str("role")
	if !validRole(role) {
		return fmt.Errorf("%w: an external block of task %s names role %s, which is no role", ErrMalformedEvent, e.TaskID, m["role"])
	}
	if err := requireMembers(e, m, present, "reason"); err != nil {
		return err
	}
	provider, ok := m.str("provider")
	reason, _ := m.str("reason")
	if !ok || (strings.TrimSpace(provider) == "" && (reason != noArchitectObtained || role != "architect")) {
		return fmt.Errorf("%w: an external block of task %s names no provider", ErrMalformedEvent, e.TaskID)
	}
	retryAt, stated := m.str("retry_at")
	switch state, _ := m.str("retry_at_state"); state {
	case retryAtUnknown:
		if m.has("retry_at") {
			return fmt.Errorf("%w: an external block of task %s states a retry time it calls UNKNOWN", ErrMalformedEvent, e.TaskID)
		}
	case retryAtKnown:
		if !stated || !validInstant(retryAt) {
			return fmt.Errorf("%w: an external block of task %s calls its retry time KNOWN and states none", ErrMalformedEvent, e.TaskID)
		}
	default:
		return fmt.Errorf("%w: an external block of task %s states retry state %s, which is neither KNOWN nor UNKNOWN",
			ErrMalformedEvent, e.TaskID, m["retry_at_state"])
	}
	return optionalString(e, m, "detail")
}

// owedArchitectReplan is the one thing a non-converged task owes
// (workflow.OwedArchitectReplan).
const owedArchitectReplan = "architect_replan"

// notConvergedPayload is an owed re-plan: its own task, the implementers that
// each spent the review budget, that budget, and what is owed.
func notConvergedPayload(e event.Event, m payloadMembers, present bool) error {
	if err := exactMembers(e, m, present, "task_id", "implementers", "review_cycles", "owed"); err != nil {
		return err
	}
	if err := ownTask(e, m, "task_id"); err != nil {
		return err
	}
	if err := nonEmptyStrings(e, m, "implementers", true); err != nil {
		return err
	}
	if cycles, ok := m.integer("review_cycles"); !ok || cycles <= 0 {
		return fmt.Errorf("%w: a non-convergence of task %s states review cycles %s", ErrMalformedEvent, e.TaskID, m["review_cycles"])
	}
	if owed, _ := m.str("owed"); owed != owedArchitectReplan {
		return fmt.Errorf("%w: a non-convergence of task %s owes %s, which no resume delivers", ErrMalformedEvent, e.TaskID, m["owed"])
	}
	return nil
}

// reviewKinds is the closed vocabulary of why a candidate awaits review.
var reviewKinds = map[string]bool{"unanswered": true, "unobtainable": true, "observation_fault": true, "advisory": true}

// awaitingReviewPayload is an owed review: why it is owed (a review kind of
// the closed vocabulary), that no independent review happened, and the
// obligations a continuation must honour, with the request and candidate
// binding the review was asked under.
func awaitingReviewPayload(e event.Event, m payloadMembers, present bool) error {
	if err := exactMembers(e, m, present, "review_kind", "independent_review", "obligations",
		"request_id", "request_comment", "conversation", "base", "candidate_digest", "candidate_tree", "review_commit",
		"waited", "review_attempt", "reviewers_tried", "handoff", "error", "observed", "observations"); err != nil {
		return err
	}
	if kind, _ := m.str("review_kind"); !reviewKinds[kind] {
		return fmt.Errorf("%w: an owed review of task %s is of kind %s, which is no review kind", ErrMalformedEvent, e.TaskID, m["review_kind"])
	}
	if independent, ok := m.boolean("independent_review"); !ok || independent {
		return fmt.Errorf("%w: an owed review of task %s does not record that no independent review happened", ErrMalformedEvent, e.TaskID)
	}
	return nonEmptyStrings(e, m, "obligations", true)
}

// restorationRefusedPayload is a refused restoration: its own task, the
// authority that could not be re-established, the instrument and binding
// that failed, and the account of it.
func restorationRefusedPayload(e event.Event, m payloadMembers, present bool) error {
	if err := exactMembers(e, m, present, "task_id", "subject", "instrument", "binding", "detail", "measured"); err != nil {
		return err
	}
	if err := ownTask(e, m, "task_id"); err != nil {
		return err
	}
	if err := requireMembers(e, m, present, "subject", "instrument", "binding", "detail"); err != nil {
		return err
	}
	if m.has("measured") && !m.object("measured") {
		return fmt.Errorf("%w: a refused restoration of task %s states a measurement that is not one", ErrMalformedEvent, e.TaskID)
	}
	return nil
}

// summarized requires the record's summary, which a precondition refusal
// carries as the reason its task restores.
func summarized(e event.Event) error {
	if strings.TrimSpace(e.Summary) == "" {
		return fmt.Errorf("%w: a %s record of task %s states no reason", ErrMalformedEvent, e.Kind, e.TaskID)
	}
	return nil
}

// baseMovedRefusedPayload is a candidate whose recorded base is not the
// repository's: its own task and both bases, which differ.
func baseMovedRefusedPayload(e event.Event, m payloadMembers, present bool) error {
	if err := exactMembers(e, m, present, "TaskID", "Recorded", "Current"); err != nil {
		return err
	}
	if err := ownTask(e, m, "TaskID"); err != nil {
		return err
	}
	if err := requireMembers(e, m, present, "Recorded", "Current"); err != nil {
		return err
	}
	recorded, _ := m.str("Recorded")
	if current, _ := m.str("Current"); recorded == current {
		return fmt.Errorf("%w: a base-moved refusal of task %s names one base as both recorded and current", ErrMalformedEvent, e.TaskID)
	}
	return summarized(e)
}

// dirtyCanonicalRefusedPayload is a canonical checkout with uncommitted
// changes: the repository it names.
func dirtyCanonicalRefusedPayload(e event.Event, m payloadMembers, present bool) error {
	if err := exactMembers(e, m, present, "Repository"); err != nil {
		return err
	}
	if err := requireMembers(e, m, present, "Repository"); err != nil {
		return err
	}
	return summarized(e)
}

// grantPayload is a prospective or test-edit grant record: its world, its
// grants (an empty set is a list too), and the plan attempt it belongs to
// when it names one.
func grantPayload(e event.Event, m payloadMembers, present bool) error {
	if err := requiredObject(e, m, present); err != nil {
		return err
	}
	if _, ok := m.str("world"); !ok || !m.list("grants") {
		return fmt.Errorf("%w: a %s record of task %s states no world or no grant list", ErrMalformedEvent, e.Kind, e.TaskID)
	}
	return optionalString(e, m, "plan_attempt_id")
}

func reviewStartedPayload(e event.Event, m payloadMembers, present bool) error {
	return requireMembers(e, m, present, "candidate")
}

func reviewFindingPayload(e event.Event, m payloadMembers, present bool) error {
	if err := requireMembers(e, m, present, "id", "severity", "claim"); err != nil {
		return err
	}
	if s, _ := m.str("severity"); !validFindingSeverity(s) {
		return fmt.Errorf("%w: a review finding of task %s has severity %q", ErrMalformedEvent, e.TaskID, s)
	}
	return nil
}

// reviewCompletedPayload is a verdict: a decision of the closed vocabulary,
// about this task (its provenance names it).
func reviewCompletedPayload(e event.Event, m payloadMembers, present bool) error {
	if err := requireMembers(e, m, present, "decision"); err != nil {
		return err
	}
	if d, _ := m.str("decision"); !validReviewDecision(d) {
		return fmt.Errorf("%w: a review of task %s decides %q", ErrMalformedEvent, e.TaskID, d)
	}
	if task, ok := m.member("provenance").str("task_id"); !ok || task != e.TaskID {
		return fmt.Errorf("%w: a review recorded in task %s's record is about task %q", ErrMalformedEvent, e.TaskID, task)
	}
	return nil
}

func receiptPayload(e event.Event, _ payloadMembers, _ bool) error {
	var r receiptRecord
	if err := DecodeExactlyOne(e.Payload, &r); err != nil {
		return fmt.Errorf("%w: the receipt of task %s does not decode: %v", ErrMalformedEvent, e.TaskID, err)
	}
	if len(r.Receipt) == 0 || strings.TrimSpace(r.Completeness) == "" {
		return fmt.Errorf("%w: the receipt record of task %s carries no receipt or no completeness", ErrMalformedEvent, e.TaskID)
	}
	return nil
}

func checkpointPayload(e event.Event, _ payloadMembers, _ bool) error {
	var b CheckpointBinding
	if err := DecodeExactlyOne(e.Payload, &b); err != nil {
		return fmt.Errorf("%w: the %s record of task %s does not decode: %v", ErrMalformedEvent, e.Kind, e.TaskID, err)
	}
	if fault := checkpointBindingFault(b); fault != "" {
		return fmt.Errorf("%w: the %s record of task %s: %s", ErrMalformedEvent, e.Kind, e.TaskID, fault)
	}
	if b.TaskID != e.TaskID || !validCheckpointID(b.CheckpointID) || b.PayloadDigest == "" || b.ReplayDigest == "" {
		return fmt.Errorf("%w: the %s record of task %s binds no checkpoint of its own task", ErrMalformedEvent, e.Kind, e.TaskID)
	}
	return nil
}

func authorityAnswerPayload(e event.Event, _ payloadMembers, _ bool) error {
	return validAuthorityAnswer(e)
}

// authorityAnswer is the one short form of an AuthorityResolved payload: the
// option a person chose in an ordinary product confirmation.
type authorityAnswer struct {
	Option string `json:"option"`
}

// authorityResolution is the AuthorityResolved payload of an answer about a
// certifiability condition: authority.Resolution's fields, inline, with the
// gap it settles and the plan attempt it was asked about. Decoded exactly
// (DecodeExactlyOne), so a payload with any other member is malformed.
type authorityResolution struct {
	TaskID        string         `json:"task_id"`
	SessionID     string         `json:"session_id"`
	Domain        string         `json:"domain,omitempty"`
	BaseSHA       string         `json:"base_sha,omitempty"`
	DecidedAt     string         `json:"decided_at"`
	Question      string         `json:"question"`
	Condition     string         `json:"condition"`
	OptionID      string         `json:"option_id"`
	OptionLabel   string         `json:"option_label"`
	Scope         []string       `json:"scope,omitempty"`
	Outcome       string         `json:"outcome"`
	State         string         `json:"state"`
	CandidatePath string         `json:"candidate_path,omitempty"`
	NodeIDs       []string       `json:"node_ids,omitempty"`
	Detail        string         `json:"detail,omitempty"`
	Gap           map[string]any `json:"gap_identity,omitempty"`
	PlanAttemptID string         `json:"plan_attempt_id,omitempty"`
}

// receiptRecord is the RunReceipt payload: the receipt, its completeness and
// what it misses, and a candidate-state disagreement when there is one.
type receiptRecord struct {
	Receipt                    map[string]any `json:"receipt"`
	Completeness               string         `json:"completeness"`
	Missing                    []string       `json:"missing"`
	CandidateStateDisagreement string         `json:"candidate_state_disagreement,omitempty"`
}

// validSemantics is THE canonical semantic test of one governed session
// event's kind (RULING-193 A), read from governedKinds: the kind is one the
// registry names, the event is attributed to a source that may record it,
// its payload is absent or one JSON object, a task the payload names is the
// event's own, and the kind's own payload test holds. Generic append
// (authorizeAppend), lineage reconstruction (step), classification
// (ClassifyTaskHistory) and every history consumer that acts on a record's
// kind (ValidGovernedEvent) apply this one test.
func validSemantics(e event.Event) error {
	rule, ok := governedKinds[e.Kind]
	if !ok {
		return fmt.Errorf("%w: a task event of kind %q is not a governed kind", ErrMalformedEvent, e.Kind)
	}
	if !rule.sources[e.Source] {
		return fmt.Errorf("%w: a %s event of task %s is attributed to %q, which may not record it",
			ErrMalformedEvent, e.Kind, e.TaskID, e.Source)
	}
	m, present, err := decodePayload(e.Payload)
	if err != nil {
		return fmt.Errorf("%w: the %s payload of task %s is not one JSON object: %v", ErrMalformedEvent, e.Kind, e.TaskID, err)
	}
	if task, ok := m.str("task_id"); m.has("task_id") && (!ok || (task != "" && task != e.TaskID)) {
		return fmt.Errorf("%w: a %s event recorded in task %s's record names task %q", ErrMalformedEvent, e.Kind, e.TaskID, task)
	}
	return rule.payload(e, m, present)
}

// validAuthorityAnswer is validSemantics for an AuthorityResolved record: it
// is exactly one of the two answer forms, and names the option chosen; an
// answer about a condition names its own task and the outcome chosen.
func validAuthorityAnswer(e event.Event) error {
	var short authorityAnswer
	if DecodeExactlyOne(e.Payload, &short) == nil {
		if strings.TrimSpace(short.Option) == "" {
			return fmt.Errorf("%w: an authority answer of task %s names no option", ErrMalformedEvent, e.TaskID)
		}
		return nil
	}
	var r authorityResolution
	if err := DecodeExactlyOne(e.Payload, &r); err != nil {
		return fmt.Errorf("%w: an authority answer of task %s is neither answer form: %v", ErrMalformedEvent, e.TaskID, err)
	}
	switch {
	case r.TaskID != e.TaskID:
		return fmt.Errorf("%w: an authority answer in task %s's record answers task %q", ErrMalformedEvent, e.TaskID, r.TaskID)
	case r.SessionID != e.SessionID:
		return fmt.Errorf("%w: an authority answer recorded by session %s of task %s was decided in session %q",
			ErrMalformedEvent, e.SessionID, e.TaskID, r.SessionID)
	case strings.TrimSpace(r.OptionID) == "":
		return fmt.Errorf("%w: an authority answer of task %s names no option", ErrMalformedEvent, e.TaskID)
	case !validAuthorityOutcome(r.Outcome):
		return fmt.Errorf("%w: an authority answer of task %s chooses outcome %q, which is no outcome", ErrMalformedEvent, e.TaskID, r.Outcome)
	case !validResolutionState(r.State):
		return fmt.Errorf("%w: an authority answer of task %s states persistence %q, which is no persistence state", ErrMalformedEvent, e.TaskID, r.State)
	case strings.TrimSpace(r.Question) == "" || strings.TrimSpace(r.DecidedAt) == "":
		return fmt.Errorf("%w: an authority answer of task %s names no question or no decision time", ErrMalformedEvent, e.TaskID)
	}
	return nil
}

// THE TASKLESS DIAGNOSTIC REGISTRY (RULING-198). A record of no task carries
// no task authority, so it is never exempt from the registry: it must be a
// diagnostic of diagnosticKinds -- the closed, exhaustive vocabulary of the
// kinds that may name no task, each with the sources that may record it and
// the canonical structure of its payload -- and well framed as a diagnostic
// (validDiagnosticFraming). Every other record of no task -- an unknown kind,
// a receipt, a terminal, a lineage transition, any governed kind -- is
// malformed and fails closed.
var diagnosticKinds = map[event.Kind]kindRule{
	// The one taskless report the engine makes: a fact about the process --
	// a behavioral outcome that could not be filed -- carried in its message.
	event.Status: {sources: systemOnly, payload: absentPayload},
}

// absentPayload is the payload of a kind that carries everything it reports
// in its message: none at all.
func absentPayload(e event.Event, _ payloadMembers, present bool) error {
	if present {
		return fmt.Errorf("%w: a %s diagnostic carries a payload", ErrMalformedEvent, e.Kind)
	}
	return nil
}

// validDiagnosticFraming is validFraming for a record of no task: it names no
// task, and names its kind and the session that wrote it, carries an event ID
// and a timestamp, and is attributed to a source of the closed vocabulary.
func validDiagnosticFraming(e event.Event) error {
	switch {
	case e.TaskID != "":
		return fmt.Errorf("%w: a %s record of task %s is not a diagnostic", ErrMalformedEvent, e.Kind, e.TaskID)
	case strings.TrimSpace(string(e.Kind)) == "":
		return fmt.Errorf("%w: a diagnostic names no kind", ErrMalformedEvent)
	case strings.TrimSpace(e.SessionID) == "":
		return fmt.Errorf("%w: a %s diagnostic names no session", ErrMalformedEvent, e.Kind)
	case strings.TrimSpace(e.ID) == "":
		return fmt.Errorf("%w: a %s diagnostic carries no event ID", ErrMalformedEvent, e.Kind)
	case e.Time.IsZero():
		return fmt.Errorf("%w: a %s diagnostic carries no timestamp", ErrMalformedEvent, e.Kind)
	case !eventSources[e.Source]:
		return fmt.Errorf("%w: a %s diagnostic is attributed to %q, which is no source", ErrMalformedEvent, e.Kind, e.Source)
	}
	return nil
}

// ValidDiagnosticEvent is THE canonical test of a record of no task: it is
// well framed as a diagnostic, of a kind diagnosticKinds names, attributed to
// a source that may record that kind, and its payload is the kind's. Generic
// append applies it to every record of no task (authorizeAppend), and the
// engine's one diagnostic path applies it before it publishes one with no
// Store to record it in.
func ValidDiagnosticEvent(e event.Event) error {
	if err := validDiagnosticFraming(e); err != nil {
		return err
	}
	rule, ok := diagnosticKinds[e.Kind]
	if !ok {
		return fmt.Errorf("%w: a record of no task of kind %q is not a diagnostic kind", ErrMalformedEvent, e.Kind)
	}
	if !rule.sources[e.Source] {
		return fmt.Errorf("%w: a %s diagnostic is attributed to %q, which may not record it", ErrMalformedEvent, e.Kind, e.Source)
	}
	m, present, err := decodePayload(e.Payload)
	if err != nil {
		return fmt.Errorf("%w: the %s diagnostic's payload is not one JSON object: %v", ErrMalformedEvent, e.Kind, err)
	}
	return rule.payload(e, m, present)
}

// ValidGovernedEvent is the canonical test a consumer of a task's history
// applies before acting on a record by its kind: the record is well framed
// (validFraming) and its kind's semantics hold (validSemantics). A record
// that fails it is malformed: it satisfies, clears and settles nothing.
func ValidGovernedEvent(e event.Event) error {
	if err := validFraming(e); err != nil {
		return err
	}
	return validSemantics(e)
}

// validTaskRoot is the canonical test of whether e can be a task's
// TaskCreated root, and so prove its HolderSessionID: a well-framed
// (validFraming) TaskCreated record attributed to a source that creates
// tasks. The holder's SessionID is only required to be named, so a holder
// minted by an earlier identity scheme still proves itself.
func validTaskRoot(e event.Event) error {
	if e.Kind != event.TaskCreated {
		return fmt.Errorf("%w: task %s's root is a %s record", ErrMalformedTaskRoot, e.TaskID, e.Kind)
	}
	if strings.TrimSpace(e.TaskID) != "" && strings.TrimSpace(e.SessionID) == "" {
		return fmt.Errorf("%w: task %s", ErrSessionlessRoot, e.TaskID)
	}
	if err := validFraming(e); err != nil {
		return fmt.Errorf("%w: %w", ErrMalformedTaskRoot, err)
	}
	if !taskRootSources[e.Source] {
		return fmt.Errorf("%w: task %s's root is attributed to %q, which does not create tasks",
			ErrMalformedTaskRoot, e.TaskID, e.Source)
	}
	// Its kind's payload, as every governed record's (validSemantics): a
	// root carries none.
	if err := validSemantics(e); err != nil {
		return fmt.Errorf("%w: %w", ErrMalformedTaskRoot, err)
	}
	return nil
}

// step is THE ONE semantic authority over the records that establish a
// task's lineage (RULING-193 A). It extends l with e, at record position at,
// or refuses it: a TaskCreated record must be the task's only root and pass
// validTaskRoot; a SessionLineageBound transition must be well framed
// (validFraming), follow the root, be the system's, decode to exactly one
// binding that is the next transition l owes (admits), and be written by the
// session it makes current. Reconstruction (TaskSessionLineage), a lineage
// binding (Store.BindSessionLineage, which steps the staged transition) and
// a generic root append (authorize) all go through it.
func (l *SessionLineage) step(e event.Event, at int) error {
	if e.TaskID != l.TaskID {
		return fmt.Errorf("%w: a %s record of task %q is not task %s's", ErrSessionLineage, e.Kind, e.TaskID, l.TaskID)
	}
	switch e.Kind {
	case event.TaskCreated:
		if l.Root >= 0 {
			return fmt.Errorf("%w: task %s records its creation more than once, so it has no single root", ErrSessionLineage, l.TaskID)
		}
		if err := validTaskRoot(e); err != nil {
			return err
		}
		l.Root, l.HolderSessionID = at, e.SessionID
		l.Segments = []SessionLineageSegment{{SessionID: e.SessionID, From: at}}
		return nil
	case SessionLineageBound:
		if err := validFraming(e); err != nil {
			return fmt.Errorf("%w: a session-lineage transition is malformed: %w", ErrSessionLineage, err)
		}
		if l.Root < 0 {
			return fmt.Errorf("%w: a session-lineage transition of task %s precedes its root", ErrSessionLineage, l.TaskID)
		}
		if e.Source != event.SourceSystem {
			return fmt.Errorf("%w: a session-lineage transition of task %s is attributed to %q, not the system",
				ErrSessionLineage, l.TaskID, e.Source)
		}
		var b SessionLineageBinding
		if err := DecodeExactlyOne(e.Payload, &b); err != nil {
			return fmt.Errorf("%w: a session-lineage transition of task %s is malformed: %v", ErrSessionLineage, l.TaskID, err)
		}
		if err := l.admits(b); err != nil {
			return err
		}
		if e.SessionID != b.CurrentSessionID {
			return fmt.Errorf("%w: a session-lineage transition of task %s making %q current was written by session %q",
				ErrSessionLineage, l.TaskID, b.CurrentSessionID, e.SessionID)
		}
		l.Segments = append(l.Segments, SessionLineageSegment{SessionID: b.CurrentSessionID, From: at})
		return nil
	}
	return fmt.Errorf("%w: a %s record does not establish lineage", ErrSessionLineage, e.Kind)
}

// admits refuses a transition that is not exactly the next one l owes.
func (l SessionLineage) admits(b SessionLineageBinding) error {
	switch {
	case b.TaskID != l.TaskID:
		return fmt.Errorf("%w: a session-lineage transition in task %s's record names task %q", ErrSessionLineage, l.TaskID, b.TaskID)
	case b.Relation != SessionLineageRelationResume:
		return fmt.Errorf("%w: a session-lineage transition of task %s states relation %q, which is not the canonical continuation",
			ErrSessionLineage, l.TaskID, b.Relation)
	case b.HolderSessionID != l.HolderSessionID:
		return fmt.Errorf("%w: a session-lineage transition of task %s names holder %q, but the task's root was written by %q",
			ErrSessionLineage, l.TaskID, b.HolderSessionID, l.HolderSessionID)
	case b.ParentSessionID != l.Tip():
		return fmt.Errorf("%w: a session-lineage transition of task %s continues %q, but the tip there is %q; "+
			"a stale or competing transition binds nothing", ErrSessionLineage, l.TaskID, b.ParentSessionID, l.Tip())
	case strings.TrimSpace(b.CurrentSessionID) == "":
		return fmt.Errorf("%w: a session-lineage transition of task %s makes no session current", ErrSessionLineage, l.TaskID)
	case l.Includes(b.CurrentSessionID):
		return fmt.Errorf("%w: a session-lineage transition of task %s makes %q current, which already belongs to its lineage",
			ErrSessionLineage, l.TaskID, b.CurrentSessionID)
	}
	return nil
}

// authorizeAppend decides, against record as it stands under the record lock,
// whether e may be appended to it. It is the one actor check of every generic
// append (Store.Append and AppendDurable), whatever e's kind:
//
//   - A session-lineage transition is never a generic append: only
//     Store.BindSessionLineage writes one.
//   - A record of no task carries no task authority: it must be a
//     diagnostic of the closed taskless registry (ValidDiagnosticEvent), or
//     it is refused.
//   - Every record of a task is well framed (validFraming) and its kind's
//     semantics hold (validSemantics); a malformed one is refused whoever
//     writes it.
//   - A task with no root yet has no lineage and no current session. Its
//     TaskCreated becomes the root only when step admits it; anything else
//     recorded before it is pre-root and never gains an owner
//     (ClassifyTaskHistory), so it is refused.
//   - A task whose recorded lineage cannot be established admits nothing.
//   - A task with a root admits a record only from its current tip. An
//     unrelated, unbound, superseded or competing session is refused, and so
//     is a second root.
func authorizeAppend(record []event.Event, e event.Event) error {
	if e.Kind == SessionLineageBound {
		return fmt.Errorf("%w: a %s record of task %q is written only by Store.BindSessionLineage",
			ErrSessionAppendRefused, e.Kind, e.TaskID)
	}
	if e.TaskID == "" {
		if err := ValidDiagnosticEvent(e); err != nil {
			return fmt.Errorf("%w: %w", ErrSessionAppendRefused, err)
		}
		return nil
	}
	if err := validFraming(e); err != nil {
		if e.Kind == event.TaskCreated {
			err = validTaskRoot(e)
		}
		return fmt.Errorf("%w: %w", ErrSessionAppendRefused, err)
	}
	// A root's semantics are validTaskRoot's, which step applies below.
	if e.Kind != event.TaskCreated {
		if err := validSemantics(e); err != nil {
			return fmt.Errorf("%w: %w", ErrSessionAppendRefused, err)
		}
	}
	l, err := TaskSessionLineage(record, e.TaskID)
	if err != nil {
		if isNoTaskRoot(err) && e.Kind == event.TaskCreated {
			// The task's root: the one record a task with no lineage admits,
			// and only one step admits.
			root := SessionLineage{TaskID: e.TaskID, Root: -1}
			if err := root.step(e, len(record)); err != nil {
				return fmt.Errorf("%w: the TaskCreated root of task %s is refused: %w", ErrSessionAppendRefused, e.TaskID, err)
			}
			return nil
		}
		return fmt.Errorf("%w: a %s record of task %s by session %s: %w", ErrSessionAppendRefused, e.Kind, e.TaskID, orNoSession(e.SessionID), err)
	}
	switch {
	case e.Kind == event.TaskCreated:
		return fmt.Errorf("%w: task %s already has its root; a second creation is refused", ErrSessionAppendRefused, e.TaskID)
	case e.SessionID != l.Tip():
		return fmt.Errorf("%w: session %s is not task %s's current session, which is %s; refused %s",
			ErrSessionAppendRefused, orNoSession(e.SessionID), e.TaskID, l.Tip(), e.Kind)
	}
	return nil
}

// HistoryClass is the canonical classification of one historical record of a
// task in its holder ledger. The vocabulary is closed and read by membership.
type HistoryClass string

const (
	// HistoryLineageMember is a record written by the session that was the
	// task's current session at its position: the root's holder or a later
	// bound session. A descendant may consume it.
	HistoryLineageMember HistoryClass = "lineage_member"
	// HistoryForeign is a record of the task written at or after the root by
	// a session that was not current there: unrelated, unbound or superseded.
	HistoryForeign HistoryClass = "foreign"
	// HistoryPreRoot is a record with no task-session owner: it precedes the
	// task's root, or the record holds no root at all.
	HistoryPreRoot HistoryClass = "pre_root"
	// HistoryMalformed is every record of a task whose lineage cannot be
	// established, and any record of it that is not a valid governed event
	// (ValidGovernedEvent).
	HistoryMalformed HistoryClass = "malformed"
)

// ClassifiedEvent is one record of a task, where it stands in the record, and
// its class. Writer is the exact SessionID the record was written under, which
// lineage never rewrites.
type ClassifiedEvent struct {
	Index  int
	Event  event.Event
	Writer string
	Class  HistoryClass
}

// ClassifyTaskHistory is the one canonical answer to whether each record of
// taskID in a holder ledger belongs to the task's valid session lineage. It
// returns the lineage it classified against, and an error -- with every record
// classified -- when there is none: ErrNoTaskRoot leaves every record pre-root,
// and ErrSessionLineage leaves malformed every record from the refused record
// on. A record that physically precedes the refused one is classified exactly
// as it would be without the later malformed state, against the lineage
// validated up to it: a malformed transition establishes nothing, and it
// erases nothing that precedes it either (RULING-195). The lineage itself is
// returned only without error: a consumer that needs a current lineage acts on
// none when there is an error.
func ClassifyTaskHistory(record []event.Event, taskID string) ([]ClassifiedEvent, SessionLineage, error) {
	l, refusedAt, err := reconstructLineage(record, taskID)
	// malformedFrom is the first position the lineage error reaches: the
	// refused record itself. What precedes it is classified against the
	// lineage validated up to it (l, the partial reconstruction), so a later
	// malformed record erases no independently valid earlier evidence; only a
	// refused root leaves nothing before it owned.
	malformedFrom := -1
	if err != nil && !isNoTaskRoot(err) {
		malformedFrom = refusedAt
	}
	var out []ClassifiedEvent
	for i, e := range record {
		if e.TaskID != taskID {
			continue
		}
		c := ClassifiedEvent{Index: i, Event: e, Writer: e.SessionID}
		switch {
		case err != nil && isNoTaskRoot(err):
			c.Class = HistoryPreRoot
		case malformedFrom >= 0 && i >= malformedFrom:
			c.Class = HistoryMalformed
		case ValidGovernedEvent(e) != nil:
			c.Class = HistoryMalformed
		case l.OwnerAt(i) == "":
			// A position the root does not reach has no owner.
			c.Class = HistoryPreRoot
		case e.SessionID == l.OwnerAt(i):
			c.Class = HistoryLineageMember
		default:
			c.Class = HistoryForeign
		}
		out = append(out, c)
	}
	if err != nil {
		return out, SessionLineage{}, err
	}
	return out, l, nil
}

func orNoSession(id string) string {
	if strings.TrimSpace(id) == "" {
		return "(none)"
	}
	return id
}

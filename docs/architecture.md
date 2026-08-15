# Sensei Code Architecture

**Status:** governing architecture for the Go implementation  
**Product:** `globulario/sensei-code`

## 1. Purpose

Sensei Code is the local interactive product surface for governed AI software development with Sensei.

It is intentionally **not a coding agent**. It is an autonomous orchestrator that coordinates an architect, bounded implementation workers, reviewers, Git worktrees, local tools, and the full Sensei governance model.

The product goal is a terminal experience comparable to Claude Code, Codex, Cursor Agent, or Pi while preserving a fundamentally different authority structure underneath.

```text
user intent
    |
    v
Sensei workspace identity / briefing / preflight
    |
    v
architectural authority
    |
    v
bounded plan
    |
    v
isolated worker candidate
    |
    v
Sensei evaluation / evidence
    |
    v
architectural review
    |
    v
Sensei admission
    |
    v
apply exact admitted artifact
    |
    v
Sensei verification / closure / completion
    |
    v
publication according to configured human policy
```

## 2. Core separation

Sensei Code preserves five independent roles:

1. **Sensei** owns architectural truth, governance semantics, evidence, admission, and closure.
2. **Sensei Code** owns orchestration, local process lifecycle, worktree lifecycle, normalized events, UI, and authority routing.
3. **Architect/reviewer models** own bounded architectural decisions and recommendations within delegated architectural authority.
4. **Workers** own bounded implementation inside isolated candidate workspaces.
5. **The human** owns authority that has not been delegated, especially changes to human-owned intent, policy, trust, and publication boundaries.

Git owns repository object identity and history mechanics. GitHub may provide collaboration, CI, and publication, but neither is architectural authority.

## 3. Sensei is upstream authority

Sensei Code consumes public Sensei contracts. It must not reproduce weaker private versions of them.

Sensei owns, among other things:

- checkout and repository-domain identity
- graph authority and freshness
- intents, invariants, contracts, failure modes, and forbidden fixes
- briefing, impact, preflight, and edit checks
- task and convergence state
- investigation and evidence coverage
- admission decisions and change envelopes
- admission verification
- proof obligations
- closure and terminal completion truth
- immutable receipts and provenance

The primary integration boundary is the existing `awareness-mcp` JSON-RPC stdio bridge. Sensei Code uses typed MCP tools and structured content rather than scraping Sensei prose or linking against internal Sensei Go packages.

This boundary lets Sensei evolve independently while keeping its semantics canonical.

## 4. Authority model

The application distinguishes **capability** from **authority**.

A capability says what a process can physically do. Authority says who is entitled to make a decision.

### 4.1 Level 1: execution authority

Sensei Code owns routine workflow execution inside the configured local capability envelope.

Examples:

- inspect repository files and history
- query Sensei
- create isolated worktrees
- invoke workers
- build, test, and lint
- capture evidence
- audit diffs
- retry a failed worker
- ask a second worker to investigate
- request bounded revisions from the same worker
- discard a non-converging candidate

These actions do not require interactive permission prompts when their local capability is granted.

### 4.2 Level 2: architectural authority

The architect is delegated architectural authority for normal design decisions that preserve existing human-owned intent and governed contracts.

Examples:

- decide which existing component should own a responsibility
- choose among implementations compatible with governing invariants
- refine scope and proof requirements
- decide whether an internal refactor is warranted
- resolve an implementation-review architectural question
- require additional tests or evidence

The architect must use repository evidence and Sensei context. Model memory is not project authority.

### 4.3 Level 3: human authority

The workflow pauses only when a decision would cross authority the architect does not own.

Examples:

- change human-owned product intent
- change or retire an invariant
- intentionally break an externally meaningful contract
- alter a security/trust boundary
- expand publication/destructive authority
- resolve an explicitly human-owned policy choice
- resolve authority ownership that Sensei cannot establish

The interaction is a small numbered choice, normally 1/2/3. The selected option becomes an input to the architect, which then produces a bounded plan and the same workflow resumes.

### 4.4 Authority failure is not worker failure

A build failure, test failure, malformed model response, agent crash, or weak candidate is not inherently a Level-3 event.

The default recovery ladder is:

```text
failure
  -> classify
  -> collect evidence
  -> retry/rebrief
  -> bounded repair
  -> alternate worker
  -> architect resolution
  -> human only if authority boundary is genuine
```

This is a defining product property.

## 5. Capability envelope

Repository-local configuration grants routine physical capabilities once.

Default local capabilities:

```text
read repository      yes
write candidates     yes
create worktrees     yes
run builds           yes
run tests             yes
local commit          yes
push                  no
force push            no
production deploy     no
```

Capabilities are not silently widened by agents.

Worker execution is additionally bounded by worktree placement, provider sandbox mechanisms when available, and the architectural contract supplied in the prompt. Stronger OS-level sandboxing is a planned hardening layer for providers that cannot mechanically enforce the complete candidate boundary themselves.

## 6. Process architecture

```text
+------------------------------------------------------------------+
|                         Bubble Tea TUI                            |
| conversation | status | authority | compact evidence | prompt    |
+--------------------------------+---------------------------------+
                                 |
                                 v
+------------------------------------------------------------------+
|                        Workflow Engine                            |
| task state | recovery | authority routing | provider scheduling  |
+-------+----------------+----------------+-------------------------+
        |                |                |
        v                v                v
+---------------+  +-------------+  +------------------+
| Sensei MCP    |  | Agent       |  | Git/worktree     |
| client        |  | adapters    |  | manager          |
+-------+-------+  +------+------+  +--------+---------+
        |                 |                  |
        v                 v                  v
 awareness-mcp      codex / claude          git
        |
        v
+------------------------------------------------------------------+
|                         Sensei Core                               |
| graph | task | evidence | admission | verification | closure     |
+------------------------------------------------------------------+
```

The event bus crosses these components. UI rendering never consumes provider stdout as its source of truth.

## 7. Repository layout

The implementation starts small and package-oriented:

```text
cmd/sensei-code/
    main.go

internal/
    agent/       provider-independent CLI agent adapter
    authority/   execution / architectural / human decision model
    config/      local provider and capability configuration
    doctor/      readiness checks
    event/       normalized events and in-process bus
    gitx/        repository/worktree mechanics
    processx/    child-process execution and streaming
    sensei/      MCP stdio client
    session/     local JSONL event persistence
    tui/         Bubble Tea presentation
    workflow/    autonomous task state machine
```

The package structure may evolve, but semantic ownership must remain explicit.

## 8. Sensei MCP boundary

Sensei's `awareness-mcp` is started as a child process using direct argv, not shell interpolation.

Sensei Code implements MCP JSON-RPC framing over stdio and performs the normal initialize handshake. It consumes `structuredContent` and keeps human text only as presentation evidence.

The expected surface includes:

```text
sensei_workspace_status
awareness_briefing
awareness_impact
awareness_resolve
awareness_query
awareness_metadata
awareness_preflight
awareness_edit_check
awareness_audit_diff
task_status
advance_task
task_briefing
sensei_workspace_admit_change
sensei_workspace_verify_admission
complete_task
inspect_terminal
recover_projections
awareness_investigate
awareness_evidence_coverage
awareness_candidates
awareness_challenge
```

`sensei-code doctor` checks that the minimum required subset is available before a run.

### 8.1 No fake green state

If Sensei returns unavailable, stale, refused, waiting, uncertifiable, scope-violated, or incomplete, Sensei Code preserves that meaning. It does not translate lack of proof into success.

## 9. Provider model

Providers are adapters, not authorities.

The initial role assignment is:

```text
architect:       Codex / OpenAI, read-only
reviewer:        Codex / OpenAI, read-only
worker primary:  Claude Code, candidate workspace
worker fallback: Codex, candidate workspace
```

Provider authentication remains owned by the provider CLI.

### 9.1 Architect protocol

The architect must return a bounded machine decision:

```json
{
  "decision": "proceed",
  "summary": "...",
  "plan": "..."
}
```

or a genuine human escalation:

```json
{
  "decision": "escalate",
  "summary": "...",
  "human_question": "...",
  "recommendation": "1",
  "options": [
    {"id": "1", "label": "..."},
    {"id": "2", "label": "..."},
    {"id": "3", "label": "Stop the task"}
  ]
}
```

Sensei Code normalizes human option IDs to the terminal's 1/2/3 interface. Model-supplied IDs are not authority.

Malformed responses fail closed and may be automatically retried.

### 9.2 Reviewer protocol

The reviewer returns one of:

```text
accept
revise
escalate
```

`revise` feeds bounded repair instructions to the current worker. `escalate` routes to the architect, not directly to the human.

Reviewer acceptance is not Sensei admission.

## 10. Candidate isolation

Each worker receives a separate Git worktree created from the exact current base.

Worktrees deliberately live beside the canonical repository:

```text
/path/project/
/path/.project.sensei-code-worktrees/<task>/<worker>/
```

They are not stored inside `<repo>/.sensei-code`, because session state and candidate mutation state are different domains.

Failed candidates may be retained temporarily as diagnostic evidence. Lifecycle/garbage-collection policy will be added once sealed candidate identity and admission application are wired.

## 11. Autonomous task state machine

The first vertical slice implements:

```text
TASK
  |
  v
workspace identity
  |
  v
Sensei preflight
  |
  v
ARCHITECT
  | proceed
  |-------------------------------+
  | escalate                      |
  v                               |
HUMAN 1/2/3                       |
  |                               |
  +----------> ARCHITECT ----------+
                  |
                  v
              bounded plan
                  |
                  v
             worker candidate
                  |
                  v
          Sensei diff audit
                  |
                  v
               REVIEW
          +-------+--------+
          |       |        |
       accept   revise   escalate
          |       |        |
          |       +-> worker
          |                |
          |             architect
          |                |
          +----------------+
                  |
                  v
      candidate ready for governed admission
```

If the primary worker does not converge within the configured review budget, Sensei Code automatically tries the next configured bounded worker.

## 12. Admission/apply boundary

The workflow must not equate a good candidate with an admitted change.

Sensei's canonical workspace admission contract preserves independent facts:

- whether an exact action may be attempted
- the admitted change envelope
- whether an observed change stayed within that envelope
- whether correctness is certified
- remaining proof/runtime obligations

The next implementation slice therefore must:

1. bind the candidate to the canonical Sensei task/convergence artifacts;
2. compose the exact admission request required by Sensei's existing owner;
3. obtain `sensei.workspace.admission.v1` decision evidence;
4. apply only the exact admitted artifact;
5. call canonical admission verification on the observed result;
6. satisfy required tests/proof/runtime evidence;
7. call Sensei terminal completion rather than inventing completion locally.

Until this is wired, the engine terminates at **candidate ready for governed admission**. That is an intentional fail-closed boundary.

## 13. Event architecture

Every important action becomes a structured event:

```text
task.created
status
output
sensei.result
agent.started
agent.finished
candidate.changed
candidate.audited
authority.required
authority.resolved
workflow.completed
workflow.failed
```

Events include session identity, task identity, source, kind, summary, and optional structured payload.

They are:

- published to the TUI through an in-process bus;
- persisted as JSONL under the local session directory;
- suitable for future JSON mode, IDE integration, CI, telemetry, or richer UIs.

Raw worker output is evidence/debug material, not the normal user interface.

## 14. TUI

The TUI uses the Charm v2 family:

```text
Bubble Tea v2   event/update/view engine
Bubbles v2      textarea and future progress/viewport components
Lip Gloss v2    terminal styling and layout
```

The conversation is the main interface. A persistent prompt sits at the bottom. Agent raw activity is collapsed by default and toggled with `Ctrl+O`.

Stable actor markers:

```text
◆ SENSEI
◈ ARCHITECT
◈ REVIEWER
● CLAUDE
● CODEX
● GIT
⚑ HUMAN AUTHORITY
```

Color reinforces meaning but cannot be the sole semantic signal.

## 15. Persistence

Local UX state lives under:

```text
<repo>/.sensei-code/
    config.json
    sessions/<session-id>/events.jsonl
```

This directory is not architectural authority and should be ignored by Git.

Project architectural truth remains in Sensei-owned repository sources and Sensei receipts.

## 16. Failure semantics

The workflow fails closed when:

- required Sensei capabilities are unavailable;
- workspace identity cannot be established;
- Sensei preflight fails;
- the architect cannot produce a bounded decision;
- all bounded workers fail to converge;
- Sensei audit fails;
- admission or verification later refuses/uncertifies the change.

The workflow does not fail merely because one worker failed. Provider failure is a recoverable execution event until the bounded recovery policy is exhausted.

## 17. Security

Initial security boundaries:

- no shell interpolation for process execution;
- model provider authentication remains external;
- candidates use isolated worktrees;
- architect/reviewer use read-only provider sandboxes when supported;
- push, force-push, and production deploy are disabled by default;
- human authority is explicit and numbered;
- local session state cannot manufacture Sensei receipts.

Planned hardening:

- OS-level worker sandboxing for providers whose own permission mode is insufficient as a mechanical boundary;
- resource/time budgets per worker;
- environment-variable filtering;
- network capability policy;
- sealed candidate digest lifecycle and exact replay/application.

## 18. Testing strategy

Tests should emphasize authority and failure semantics rather than only happy-path rendering.

Required classes:

- authority-level behavior
- event fanout and persistence
- worktree path/isolation invariants
- MCP framing and structured tool handling
- provider output normalization
- malformed architect/reviewer response fail-closed behavior
- worker fallback and bounded retries
- Level-3 rendezvous/resume
- Sensei unavailability/refusal propagation
- eventual exact candidate admission/apply/verify replay

The TUI should remain a projection of tested workflow state so rendering bugs cannot redefine governance semantics.

## 19. Milestones

### M0: Go foundation

- module and package layout
- event bus
- authority types
- config/capability envelope
- process runner
- Git/worktree manager

### M1: interactive autonomous candidate loop

- Charm TUI
- Sensei MCP client
- architect/reviewer adapters
- Claude/Codex worker adapters
- preflight
- diff audit
- bounded revisions
- worker fallback
- human authority rendezvous
- local session log
- doctor

### M2: full governed candidate lifecycle

- canonical task/convergence binding
- sealed candidate identity
- Sensei admission request
- exact admitted apply
- admission verification
- required proof/test/runtime evidence
- terminal completion

### M3: operational polish

- resume active sessions
- worktree cleanup/leases
- slash commands
- diff/evidence views
- richer progress indicators
- model/provider selection

### M4: collaboration

- optional GitHub issue/PR/CI adapters
- publication policies
- review packets

## 20. Non-negotiable laws

1. **Sensei is governance, not another agent.**
2. **Routine execution is autonomous.**
3. **Architectural authority belongs to the architect until a human-owned boundary is reached.**
4. **Human prompts represent authority crossings, not agent nervousness.**
5. **Workers mutate candidates, not canonical authority.**
6. **Reviewer acceptance is not admission.**
7. **Scope compliance is not correctness certification.**
8. **Absence of evidence is not proof.**
9. **Local UI state is not project truth.**
10. **A governed run must be backed by the real Sensei receipts and exact bindings it claims.**

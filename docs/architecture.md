# Sensei Code Architecture

**Status:** governing architecture for the Go implementation  
**Product:** `globulario/sensei-code`

## 1. Purpose

Sensei Code is the local interactive product surface for governed AI software development with Sensei.

It is intentionally **not a coding agent**, and it is not a replacement for the coding agent a developer already uses. It is the layer underneath them: a uniform surface that carries governed architectural knowledge, task identity, and honest state into whichever agent is doing the work — and, when a change warrants it, an orchestrator that can run the full governed candidate pipeline.

The product goal is a terminal experience comparable to Claude Code, Codex, Cursor Agent, or Pi, with a layer of architectural knowledge where those tools have plain agent integration.

Sensei Code runs in two modes, described in section 3. The flow below is **governed mode**; assisted mode is the default and stops at a reviewed working tree.

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

## 3. Operating modes

Sensei Code has two operating modes over the same machinery. They differ in who drives the agent, where the agent writes, and what receipts exist at the end.

### 3.1 Assisted mode (default)

The developer keeps using their coding agent the way they use it today. Sensei Code supplies what the agent session cannot supply for itself: governed architectural context, a durable task identity, continuity across agents, and honest state.

```text
human + their agent (Claude Code / Codex / Cursor)
    |
    v
Sensei Code supplies architectural context + task identity
    |
    v
agent works in the developer's checkout
    |
    v
Sensei observes: preflight, edit checks, diff audit
    |
    v
reviewed working tree
    |
    v
human commits
```

Assisted mode does **not** replace the coding agent, does not seal a candidate, and does not request admission. Its product claim is narrower and honest:

- the agent starts with the repository's governed architectural context rather than an inferred one;
- the unit of continuity is the **task**, not the agent session, so work survives switching from one agent to another;
- Sensei's observations (preflight, edit check, diff audit, forbidden fixes) are surfaced as the work happens;
- absence, staleness, and unavailability of that context are stated rather than hidden.

This is the primary adoption surface. Most repositories and most changes should never need more than this.

### 3.2 Governed mode (opt-in)

Governed mode is the autonomous orchestrated pipeline described in the rest of this document: isolated candidate worktrees, bounded workers, deterministic diff audit, reviewer cycles, Sensei admission, exact-artifact apply, verification, and terminal completion.

It costs more (isolation, sealing, evidence, admission latency) and it buys receipts. It is selected per task, and the natural selector is the change's rigor class rather than a global preference.

### 3.3 Mode is derived from receipts, not configuration

A task is governed because the canonical Sensei task, candidate, admission, verification, and completion records exist for it — not because a config key says `governed`.

Sensei Code must make it structurally impossible for an assisted task to be presented as a governed one. Where a governed record does not exist, the surface says so in the same words Sensei would use.

### 3.4 What Sensei Code does not use

Sensei Code does **not** invoke Sensei's governed *synthesis and generation* stack (the O1–O3 interpretation → planning → generation path). Code generation belongs to the coding agents, in both modes.

This is deliberate and evidence-based. In Sensei's own grpc-go comparison, an agent working with Sensei context solved 3/3 tasks while the governed synthesis driver produced 1/3 candidates, and both of its failures were the O3 whole-file-rewrite protocol failing to scale (a 128KB snapshot), not a reasoning failure. The R1 pilot showed the same ordering at larger n (9/9 against 5/9).

The counter-evidence is preserved rather than discarded: the single candidate the synthesis driver did produce was the *more disciplined* repair — one file and four lines against the real feature flag, where the agent's accepted solve touched three files and deleted roughly a hundred test helpers. Generation quality is not the reason O3 is out of the path; protocol scaling is. If that scaling problem is solved upstream, this decision should be revisited on the evidence.

What Sensei Code **does** use, in governed mode, are Sensei's evaluation, admission, verification, and closure owners — applied to an agent-produced candidate.

### 3.5 Open contract question

Sensei's candidate evaluation and admission owners were built around candidates that Sensei's own driver generated. Whether they accept an **externally generated** candidate — an agent-produced worktree diff sealed by Sensei Code — is an open contract question against Sensei core, not a settled integration.

It must be resolved before governed mode can be called complete. Until it is resolved, governed mode terminates at *candidate ready for governed admission*, which is the fail-closed boundary already described in section 15. Sensei Code must not invent a local candidate artifact format to route around the question.

## 4. Architectural context

Assisted mode's entire value is the context it injects. That makes context quality a governance concern, not a UX concern: context that is confidently wrong makes several agents wrong in unison and faster than they would have been alone.

### 4.1 The context packet

Assembled from canonical Sensei sources for the file, component, or task in view:

```text
repository and domain identity
graph authority and generation
task identity and briefing
applicable invariants
governing contracts
known failure modes
forbidden fixes
required tests and proof obligations
impact / blast radius
relevant architectural decisions
unresolved or contested knowledge
```

### 4.2 Typed absence

Every field is one of `present`, `empty-proven`, `absent`, `stale`, or `unavailable`.

An empty panel is not an answer. "No invariants apply to this file" and "the graph has no coverage for this file" are different facts, and only the first is safe to act on. A briefing that returns nothing because the graph is behind HEAD must say that, with the generation it was answered from.

This is the same rule as section 11.1 applied to the read path, and it is the failure mode most likely to make the product actively harmful.

### 4.3 Proportional selection

Context value comes from selection, not volume. Injecting every invariant into every turn burns the agent's budget and dilutes the ones that matter.

Sensei already owns the dial: rigor class (`sensei rigor`, `docs/rigor_classes.yaml`, strictest-of-matching). How much context a file receives, and whether a change is a candidate for governed mode at all, should be a function of its rigor class.

### 4.4 Delivery

Context reaches an agent through whichever mechanism that agent actually supports:

```text
Claude Code   generated CLAUDE.md, PreToolUse briefing push, Sensei MCP tools
Codex         generated AGENTS.md, prompt preamble, MCP where supported
Cursor        rules files
other         prompt preamble
```

Sensei Code owns the assembly and the freshness guarantee. It does not own the semantics of anything in the packet.

### 4.5 Task continuity

The handoff contract is what makes the task, rather than the session, the unit of work. When a second agent picks up a task, it receives:

```text
task identity and current state
the context packet, refreshed at the current graph generation
decisions already made, and by whom
scope declared so far
work already performed, as diff and evidence
open questions
```

A second agent must never begin from a cold prompt on a task that is already underway. Proving this handoff is the first thing worth demonstrating about the product.

## 5. Readiness, install, and freshness

Sensei Code's value is proportional to the quality and freshness of the graph behind it. That makes installation and readiness a governance concern rather than a packaging chore: a stale graph does not fail loudly, it answers confidently and wrongly.

This section is likely to be the bulk of the engineering effort. The orchestration layer is thin because Sensei already owns the semantics; the edges are not.

### 5.1 The install must swallow the whole runtime

A user installing Sensei Code should not have to know that a triple store is involved.

```text
sensei-code binary
Sensei binaries
the store (bundled, versioned with the binaries)
graph state directory
provider CLIs (detected, never installed silently)
```

Any step a user must perform by hand is a step where the product fails for everyone who is not already an expert in it.

### 5.2 Freshness is a first-class state, not a health check

The graph is answered from a generation. That generation has a relationship to `HEAD` and to the working tree, and that relationship must be computed and displayed, not assumed.

```text
fresh          graph generation covers HEAD
behind         graph generation predates HEAD by N commits
uncovered      the files in view are not represented at this generation
unbuilt        no graph for this repository/domain
unavailable    the store or service cannot be reached
mismatched     graph identity does not match this checkout
```

Assisted mode must display the current state and the generation alongside any context it injects. Governed mode fails closed on anything other than `fresh` unless the human explicitly accepts a weaker state for that task.

The failure this prevents is specific and has already been observed in practice: a briefing answered from a stale baseline returns nothing for recently added code, and an empty briefing is indistinguishable from a clean one unless the surface says which it is. See section 4.2.

### 5.3 Hazards the product must absorb

These are real operational hazards of the current runtime. They are listed because a "simple orchestrator" will encounter every one of them, and because an end user cannot be expected to navigate any of them.

- **Marker binding.** A build performed without an explicit graph-marker target can rebind the marker of a running daemon. Sensei Code must never invoke a build that can reach another instance's state.
- **Shared process lifetime.** Stopping the graph service also stops the store, and restarting it can incur a silent multi-second store-recovery window during which answers are unavailable rather than wrong. That window must be a displayed state, not a hang.
- **Provenance-degrading builds.** Some build paths produce binaries whose provenance is unstamped. Readiness must detect this rather than treat any binary as equivalent.
- **Domain scope.** A combined multi-project graph answers out-of-project questions unless it is scoped. Sensei Code must resolve and pin the repository's domain before injecting anything.
- **Port and instance collision.** Readiness must bind to an explicitly identified instance. It must never stop or reconfigure a service it did not start.

### 5.4 Cold repositories

A repository with no graph is the normal first-run case, not an error.

Sensei Code guides the user through the explicit onboarding Sensei already owns (init, import/bootstrap, build, serve). It must not fabricate placeholder bindings, and it must not silently mutate governance sources to reach a green state.

Until a repository has a graph, assisted mode degrades honestly to an ordinary agent session with typed-absent context. That is a legitimate product state and must be labeled as one.

### 5.5 `doctor` is the contract

`sensei-code doctor` is the single machine-readable statement of whether the loop can run: binaries, versions, store, graph generation and freshness, domain resolution, required tool subset, provider readiness.

Every readiness fact the UI shows comes from the same computation. Two code paths answering "are we ready" will eventually disagree, and the optimistic one will win in the UI.

## 6. Sensei is upstream authority

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

## 7. Authority model

The application distinguishes **capability** from **authority**.

A capability says what a process can physically do. Authority says who is entitled to make a decision.

The three levels below describe **governed mode**, where Sensei Code drives the agents. In **assisted mode** the developer is driving, so Level 2 delegation does not apply and Sensei Code's role is to inform and observe rather than to decide. Level 1 capability limits and Level 3 crossings hold in both modes.

The human's architectural role is **intent and policy**: what the system is for, which invariants hold, which contracts are externally meaningful, where the trust boundaries are. That work is rare, durable, and belongs in Sensei rather than in a conversation. The architect model's role is **implementation architecture** within those rules.

That delegation is only as safe as Sensei's ability to check it, which is the subject of section 7.5.

### 7.1 Level 1: execution authority

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

### 7.2 Level 2: architectural authority

The architect is delegated architectural authority for normal design decisions that preserve existing human-owned intent and governed contracts.

Examples:

- decide which existing component should own a responsibility
- choose among implementations compatible with governing invariants
- refine scope and proof requirements
- decide whether an internal refactor is warranted
- resolve an implementation-review architectural question
- require additional tests or evidence

The architect must use repository evidence and Sensei context. Model memory is not project authority.

This delegation is bounded by what Sensei can certify. The architect holds authority inside the certifiable region and does not hold it outside — see section 7.5.

### 7.3 Level 3: human authority

The workflow pauses when a decision would cross authority the architect does not own, or when Sensei cannot certify the decision at all.

Examples:

- change human-owned product intent
- change or retire an invariant
- intentionally break an externally meaningful contract
- alter a security/trust boundary
- expand publication/destructive authority
- resolve an explicitly human-owned policy choice
- resolve authority ownership that Sensei cannot establish

The interaction is a small numbered choice, normally 1/2/3. The selected option becomes an input to the architect, which then produces a bounded plan and the same workflow resumes.

The answer does not stop there. See section 7.6.

### 7.4 Authority failure is not worker failure

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

### 7.5 Escalation is triggered by certifiability, not by model uncertainty

A model deciding whether to involve the human is not a control. It fails in exactly the case that matters: a confidently wrong architect does not escalate. It also produces the interruption noise section 7.4 exists to prevent, because an uncertain model escalates when nothing is actually at stake.

The trigger is therefore a property of the graph, not a self-report.

**The architect holds Level-2 authority inside the region Sensei can certify:**

| Sensei can certify | Sensei cannot certify |
|---|---|
| an invariant is violated | whether this is the right architecture for the problem |
| a change matches a known forbidden fix | whether a claim in the plan is true of the repository |
| the change stayed inside the declared envelope | whether the task itself is the wrong task |
| required tests executed against this exact result | anything in a region the graph does not cover |
| the graph is fresh and the domain is scoped | contracts that are unknown, contested, or absent |

The right column is not a defect list to be closed. Some of it is irreducible. It is the definition of where human authority actually lives.

**Escalate when any of these hold:**

```text
the decision touches a region with no graph coverage at this generation
a claim in the governing plan is contradicted by the graph
the governing contract is unknown, contested, or absent
the change would alter human-owned intent, an invariant, or a trust boundary
the graph is not fresh enough to answer the question being decided
Sensei cannot establish who owns the authority for this decision
```

This is computable, and it fails in the right direction: an unknown pulls the human in, a known does not. A model's confidence is an input to nothing.

The corollary matters as much: **outside those conditions, do not ask.** A human prompt that a certifiable rule could have answered is a defect, not caution.

### 7.6 Human answers become durable

Every Level-3 resolution is a governed write, not a conversational reply.

```text
human answers a Level-3 question
    -> the answer is proposed into the graph
       as an intent, invariant, contract, or forbidden fix
    -> the same question is certifiable next time
    -> it never reaches a human again
```

Without this, the interruption rate plateaus and the system merely stopped asking rather than learning. With it, human involvement shrinks toward genuinely new territory, which is the only version of "autonomous" worth having.

An unrecorded Level-3 answer is a bug. If the answer cannot be expressed as a governed entry, that is itself a finding — Sensei's `contract_unknown` path exists for it.

## 8. Capability envelope

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

## 9. Process architecture

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

## 10. Repository layout

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

## 11. Sensei MCP boundary

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

### 11.1 No fake green state

If Sensei returns unavailable, stale, refused, waiting, uncertifiable, scope-violated, or incomplete, Sensei Code preserves that meaning. It does not translate lack of proof into success.

## 12. Provider model

Providers are adapters, not authorities.

The initial role assignment is:

```text
architect:       Codex / OpenAI, read-only
reviewer:        Codex / OpenAI, read-only
worker primary:  Claude Code, candidate workspace
worker fallback: Codex, candidate workspace
```

Provider authentication remains owned by the provider CLI.

### 12.1 Architect protocol

The architect must return a bounded machine decision:

```json
{
  "decision": "proceed",
  "summary": "...",
  "plan": "...",
  "claims": [
    {"statement": "...", "about": "path or component", "source": "graph|repository|inference"}
  ]
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

**On `claims`.** These are the factual premises the plan rests on. They exist so that section 7.5's contradiction check has something to check: Sensei Code resolves each claim against the graph before the plan governs anything, and a contradicted claim is an escalation rather than a warning.

A plan whose premises are all `inference` is not thereby wrong, but it is uncertifiable — and uncertifiable plans do not carry Level-2 authority.

This is the smallest available guard against the failure mode Sensei's own R1 pilot identified: an interpretation becomes governing authority without ever being checked, and a single false premise loses a task the same model solves once the premise is corrected.

### 12.2 Reviewer protocol

The reviewer returns one of:

```text
accept
revise
escalate
```

`revise` feeds bounded repair instructions to the current worker. `escalate` routes to the architect, not directly to the human.

Reviewer acceptance is not Sensei admission.

## 13. Candidate isolation

Each worker receives a separate Git worktree created from the exact current base.

Worktrees deliberately live beside the canonical repository:

```text
/path/project/
/path/.project.sensei-code-worktrees/<task>/<worker>/
```

They are not stored inside `<repo>/.sensei-code`, because session state and candidate mutation state are different domains.

Failed candidates may be retained temporarily as diagnostic evidence. Lifecycle/garbage-collection policy will be added once sealed candidate identity and admission application are wired.

## 14. Task state machines

### 14.1 Assisted mode

```text
TASK
  |
  v
workspace identity + graph generation
  |
  v
context packet assembled (typed absence preserved)
  |
  v
agent session (any provider) in the developer's checkout
  |
  +--> agent switched --> handoff packet --> agent session
  |
  v
Sensei observation: preflight / edit check / diff audit
  |
  v
reviewed working tree
  |
  v
human commits
```

No candidate is sealed and no admission is requested. The task record holds identity, context provenance, decisions, and observations — and it is never reported as a governed run.

### 14.2 Governed mode

The autonomous vertical slice implements:

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

## 15. Admission/apply boundary

This section applies to **governed mode** only. Assisted mode has no admission step, and must never display one; its terminal state is a reviewed working tree owned by the human.

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

## 16. Event architecture

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

## 17. TUI

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

## 18. Persistence

Local UX state lives under:

```text
<repo>/.sensei-code/
    config.json
    sessions/<session-id>/events.jsonl
```

This directory is not architectural authority and should be ignored by Git.

Project architectural truth remains in Sensei-owned repository sources and Sensei receipts.

## 19. Failure semantics

The workflow fails closed when:

- required Sensei capabilities are unavailable;
- workspace identity cannot be established;
- Sensei preflight fails;
- the architect cannot produce a bounded decision;
- all bounded workers fail to converge;
- Sensei audit fails;
- admission or verification later refuses/uncertifies the change.

The workflow does not fail merely because one worker failed. Provider failure is a recoverable execution event until the bounded recovery policy is exhausted.

## 20. Security

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

## 21. Testing strategy

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
- context packet absence typing: `absent`, `stale`, and `unavailable` must not render as `empty-proven`
- freshness: a graph generation behind `HEAD` is reported as `behind`, never silently answered as current
- readiness: `doctor` and the UI return the same verdict from the same computation
- a cold repository reaches a labeled degraded session rather than a fabricated binding
- escalation fires on each certifiability condition in section 7.5, and does **not** fire on model-reported uncertainty alone
- a plan carrying a claim the graph contradicts escalates rather than governing
- a resolved Level-3 answer produces a governed graph entry, and the same question is certifiable on replay
- agent handoff: a second provider receives prior scope, decisions, and evidence
- an assisted task cannot be rendered with governed-run vocabulary

The TUI should remain a projection of tested workflow state so rendering bugs cannot redefine governance semantics.

## 22. Milestones

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

### M2: readiness, install, and freshness

Section 5. Sequenced ahead of assisted mode because assisted mode injects context, and context from an unknown generation is worse than no context.

- single-artifact install covering the binaries and the store
- one readiness computation behind `doctor` and the UI
- graph generation resolved and compared against `HEAD`
- freshness state displayed with every injected packet
- domain resolved and pinned before any injection
- cold-repository onboarding guidance without fabricated bindings
- never build, stop, or reconfigure an instance Sensei Code did not start

Exit condition: a developer who has never run Sensei installs Sensei Code, opens an un-onboarded repository, and reaches either a working assisted session or an accurate statement of what is missing — without hand-editing state or knowing that a triple store exists.

### M3: assisted mode

The default product surface, reusing M1's context, session, and event machinery.

- context packet assembly with typed absence and graph provenance
- rigor-proportional context selection
- per-provider delivery (CLAUDE.md / AGENTS.md / rules / preamble / MCP)
- durable task identity across agent sessions
- agent handoff packet
- Sensei observation surfaced in-session (preflight, edit check, diff audit)
- assisted tasks structurally distinguishable from governed runs

Exit condition: two different coding agents work the same task in sequence, and the second one starts from the first one's context, scope, and decisions rather than a cold prompt.

### M4: full governed candidate lifecycle

- resolve the external-candidate contract question (section 3.5) with Sensei core
- canonical task/convergence binding
- sealed candidate identity
- Sensei admission request
- exact admitted apply
- admission verification
- required proof/test/runtime evidence
- terminal completion

### M5: operational polish

- resume active sessions
- worktree cleanup/leases
- slash commands
- diff/evidence views
- richer progress indicators
- model/provider selection

### M6: collaboration

- optional GitHub issue/PR/CI adapters
- publication policies
- review packets

## 23. Non-negotiable laws

1. **Sensei is governance, not another agent.**
2. **Sensei Code does not replace the developer's coding agent.** Assisted mode is the default; the agents keep generating the code.
3. **Mode is derived from receipts, not configuration.** A task is governed because the canonical Sensei records exist, never because a setting says so.
4. **Injected context carries its provenance.** Every claim handed to an agent names the graph generation it came from, and absence is typed rather than blank.
5. **Routine execution is autonomous in governed mode.**
6. **Architectural authority belongs to the architect inside the region Sensei can certify, and nowhere else.**
7. **Escalation is triggered by certifiability, not by model uncertainty.** A model's confidence is an input to nothing. Equally: do not ask a human what a certifiable rule already answers.
8. **Every human answer becomes a governed entry.** An unrecorded Level-3 resolution is a bug, because it guarantees the same question returns.
9. **A plan's premises are checkable or the plan is uncertifiable.** Uncertifiable plans do not carry architectural authority.
10. **Human prompts represent authority crossings, not agent nervousness.**
11. **Workers mutate candidates, not canonical authority.**
12. **Reviewer acceptance is not admission.**
13. **Scope compliance is not correctness certification.**
14. **Absence of evidence is not proof.**
15. **Local UI state is not project truth.**
16. **A governed run must be backed by the real Sensei receipts and exact bindings it claims.**

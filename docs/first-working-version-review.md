# Sensei Code first working version review

**Baseline reviewed:** `main` at `d1342aa3eeaf94bfa9d80fa6a321abf62ed66922`  
**Review date:** 2026-08-16  
**Scope:** whole first Go implementation, including CLI/TUI, workflow engine, Sensei MCP boundary, assisted packets, authority model, Git/worktrees, provider execution/authentication, doctor/readiness, events/session persistence, tests, CI, and governing documentation.

## Verdict

The first working version is a strong foundation and should **not** be rewritten from scratch. The package boundaries, direct Sensei MCP integration, provider-owned authentication, isolated worktrees, event-driven TUI, and explicit refusal to treat reviewer acceptance as Sensei admission all point in the intended direction.

However, the current executable does **not yet satisfy the most important Sensei Code requirements as control-plane properties**.

The central problem is not missing UI polish. It is that several non-negotiable laws are documented correctly but are currently enforced only by model prompts, configuration flags, or prose. The next version should convert those laws into typed, mechanically checked boundaries while keeping Sensei Code thin and leaving Sensei as the owner of architectural truth, governance, evidence, admission, verification, and closure.

The redesign below preserves the existing architecture wherever possible.

---

## What should remain

These parts fit the intended product and should be evolved rather than replaced:

1. **Go as the implementation language.**
2. **Charm-based terminal UI** with Bubble Tea, Bubbles, and Lip Gloss.
3. **Conversation-first interaction** with a persistent prompt and collapsed raw agent activity.
4. **Structured event bus** as the boundary between workflow execution and presentation.
5. **Repository-local non-authoritative session state** under `.sensei-code/`.
6. **Direct argv process execution** rather than shell interpolation.
7. **Direct structured MCP connection to Sensei** rather than scraping CLI prose or importing Sensei internals.
8. **Replaceable provider adapters** and provider-owned credentials.
9. **Architect / implementor / reviewer separation.**
10. **Candidate worktrees outside the canonical checkout.**
11. **Bounded review/repair cycles and worker fallback.**
12. **Reviewer acceptance is not admission.**
13. **Assisted artifacts explicitly cannot claim governed admission.**
14. **GitHub remains collaboration/publication infrastructure, not project authority.**
15. **Honest implementation-status documentation** that stops at `candidate ready for governed admission` rather than inventing receipts that do not exist.

The overall package split is also directionally good. The redesign should refine ownership, not flatten it.

---

# P0: control-plane corrections required before calling the product requirement-conformant

## P0.1 Compute authority crossings from Sensei certifiability, not from the architect model

### Current state

`internal/workflow/engine.go` asks the architect model to return:

```json
{"decision":"proceed"|"escalate", ...}
```

and then directly trusts that choice. The current `architectureDecision` does not even carry the `claims` contract described in `docs/architecture.md`.

This means a confidently wrong architect can say `proceed` in an uncovered or contradicted region, while a nervous architect can say `escalate` for an ordinary implementation question. That is exactly the failure mode the governing architecture says must not exist.

### Required redesign

Introduce a **mechanical authority router** whose input is Sensei-backed certifiability evidence, not model confidence.

The architect may still propose a bounded plan and factual premises, but it does not decide whether the plan possesses Level-2 authority.

The architect response must include explicit claims:

```json
{
  "decision": "propose",
  "summary": "...",
  "plan": "...",
  "claims": [
    {"statement":"...", "about":"...", "source":"graph|repository|inference"}
  ]
}
```

Sensei Code then asks canonical Sensei surfaces to establish whether the plan is inside the certifiable region. Sensei Code must not reimplement the graph semantics locally.

The router should produce one of:

```text
architectural-authority-granted
human-authority-required
cannot-establish-authority
```

A model-supplied `escalate` may be treated as a request for extra investigation, but it must not itself create a Level-3 interruption when Sensei can certify the decision.

### Acceptance tests

- architect says proceed, graph coverage is absent -> human authority required;
- architect says proceed, governing contract is contradicted -> human authority required;
- architect says escalate, Sensei can certify the question -> no human prompt; resolve architecturally;
- a plan with an unverified governing premise cannot acquire Level-2 authority;
- model confidence/uncertainty text has no effect on routing;
- every Level-3 event names the exact certifiability condition that caused it.

---

## P0.2 Turn Sensei structured results into typed gates instead of advisory text

### Current state

The workflow calls `sensei_workspace_status`, `awareness_preflight`, and `awareness_audit_diff`, but the governing decisions mostly use `firstText(...)` and pass that text into model prompts.

`CallTool` currently rejects JSON-RPC/tool errors, but a semantically negative, stale, refused, malformed, incomplete, or empty `structuredContent` result can still become ordinary prompt text.

A reviewer can therefore become the effective judge of a Sensei audit instead of Sensei owning that gate.

### Required redesign

Add typed adapters over public Sensei MCP results, for example:

```text
internal/sensei/contracts.go
  WorkspaceStatus
  PreflightDecision
  DiffAuditDecision
  CertifiabilityResult
  AdmissionDecision       // later slice
  VerificationDecision    // later slice
```

The exact field mapping must follow Sensei's public structured contracts. Sensei Code must not invent alternative semantics.

Control logic consumes typed structured values. Human-readable `content[].text` remains presentation evidence only.

Required rule:

> No governance transition may depend on `firstText`, substring matching, or reviewer interpretation of a Sensei verdict.

Reject required results that are empty or structurally malformed. Preserve explicit states such as unavailable, stale, refused, waiting, incomplete, scope violation, or uncertifiable.

### Acceptance tests

- empty required `structuredContent` fails closed;
- malformed required result fails closed;
- preflight refusal prevents worker execution even if the architect says proceed;
- an audit violation cannot be overridden by reviewer `accept`;
- unavailable Sensei is not rendered as a clean result;
- text and structured verdict disagreement follows the structured contract and surfaces the discrepancy as a diagnostic.

---

## P0.3 Make assisted mode the actual default interactive product

### Current state

The documentation says assisted mode is the default adoption surface. The assisted context and handoff packages exist, but they are exposed mainly as separate `sensei-code context` and `sensei-code handoff` commands.

Launching `sensei-code` currently wires the TUI directly to `workflow.Engine.Submit`, which immediately enters the isolated governed-candidate flow and labels the UI `autonomous, governed development`.

That makes the executable disagree with its own product contract.

### Required redesign

Introduce explicit task-mode state in the coordinator:

```text
assisted   default interactive mode
governed   opt-in / rigor-selected mode backed by governed task state
```

The mode shown in the UI must be derived from real workflow/Sensei state, never from cosmetic configuration.

Assisted mode should reuse the same session, event, Sensei, provider, and TUI machinery but follow its own state machine:

```text
task identity
-> exact workspace/freshness state
-> context packet
-> provider session in developer checkout
-> live Sensei observations
-> cross-agent handoff when provider changes
-> reviewed working tree
```

Governed mode retains candidate worktrees and later admission/apply/verify.

Do not fork the product into two unrelated implementations. They should be two workflows over shared contracts.

### Acceptance tests

- plain `sensei-code` starts in assisted mode;
- assisted mode never creates candidate/admission vocabulary or receipts;
- switching to governed mode requires the canonical prerequisites for that task;
- UI always displays the actual mode and provenance;
- an assisted task cannot accidentally transition to governed vocabulary because of a local config flag.

---

## P0.4 Enforce the local capability envelope mechanically without reintroducing permission popups

### Current state

The configuration exposes capabilities such as:

```text
run_builds
run_tests
local_commit
push
force_push
production_deploy
```

but the workflow currently checks only a subset such as repository read, candidate write, and worktree creation.

The default Claude worker runs with `--permission-mode bypassPermissions`. That correctly avoids repetitive user prompts, but worktree isolation alone does not mechanically enforce `push: false`, `force_push: false`, production/network boundaries, credential exposure, or all canonical-checkout mutation boundaries.

The current capability file therefore describes intent more strongly than the execution layer enforces it.

### Required redesign

Add an **execution broker/policy layer** between workflow intent and provider/process execution.

Conceptually:

```text
workflow action
-> required capability
-> execution policy
-> provider-specific mechanical sandbox
-> process runner
```

The broker grants routine local authority once and then runs autonomously. It must not turn every build/test/edit into a human prompt.

Provider adapters must declare what they can mechanically enforce. A provider that cannot honor a required boundary must fail closed for that role rather than silently widen the envelope.

At minimum, enforce:

- canonical checkout mutation policy;
- candidate-only write boundary in governed mode;
- build/test execution capability;
- local commit capability;
- push / force-push policy;
- production-deploy prohibition;
- environment/credential exposure policy;
- bounded process lifetime/resource policy as the hardening layer lands.

### Acceptance tests

- a worker cannot push when `push=false` even if its model tries;
- a worker cannot force-push when `force_push=false`;
- governed worker cannot mutate the canonical checkout;
- disabling builds/tests is mechanically reflected in execution capability;
- no routine action asks the human merely because the provider itself is configured to prompt;
- provider inability to enforce the envelope is reported as readiness failure for that role.

---

## P0.5 Bind every governed candidate to an exact base and exact candidate identity

### Current state

A worktree is created from `HEAD`, but the workflow does not yet make exact base identity a first-class task/candidate contract.

A dirty canonical checkout is especially ambiguous: the user's visible state may differ from the `HEAD` used to create the candidate worktree.

### Required redesign

Before governed execution, establish and persist:

```text
repository identity
base commit SHA
working-tree state policy
Sensei graph generation/domain bound to that base
candidate worktree path
candidate branch/head SHA when available
candidate diff digest / sealed artifact identity when Sensei contract is available
```

Governed mode should either require a clean canonical base or use an explicitly defined snapshot mechanism. It must never silently omit uncommitted user state from the task it claims to be governing.

This becomes the substrate for the later exact admission/apply/verify lifecycle.

### Acceptance tests

- dirty canonical checkout cannot silently seed a governed candidate from a different state;
- base SHA is immutable for a candidate lifecycle;
- audit evidence names the exact candidate/base pair;
- worker fallback starts from the same declared base unless a new task generation is explicitly created.

---

## P0.6 Persist Level-3 human resolutions into Sensei-owned governance

### Current state

The current Level-3 rendezvous records the selected option as a local event and feeds it back into the architect prompt.

That resumes the run, but it does not implement the governing law that the answer becomes durable project knowledge so the same authority question is certifiable next time.

### Required redesign

After a Level-3 selection, route the resolution through the **canonical Sensei write/admission mechanism for governance knowledge**.

Do not invent a Sensei Code-owned `decisions.json` authority store.

If the current public Sensei MCP surface cannot express the required intent/invariant/contract/forbidden-fix proposal, treat that as an upstream Sensei contract gap and keep the current run honest:

```text
resolution applied to this run
project-governance persistence: unsupported / pending upstream contract
```

Do not claim learned autonomy until the governed write is verified.

### Acceptance tests

- Level-3 resolution produces verified Sensei-owned durable evidence;
- replaying the same question uses that evidence and does not reprompt the human;
- failure to persist the resolution is visible and cannot be converted into a successful durable-resolution claim.

---

## P0.7 Implement real typed absence and freshness in assisted context packets

### Current state

`internal/assist/packet.go` defines useful states such as `present`, `empty-proven`, `absent`, `stale`, and `unavailable`, but `observed(...)` currently marks every successful Sensei tool call as `Present`.

That means the type system exists but the semantic distinction that motivated it is not yet implemented.

The broader architecture also names `unbuilt` and `mismatched` readiness conditions.

### Required redesign

Create one canonical normalization from typed Sensei workspace/context results into product observation states. Reuse it in:

- assisted context packets;
- TUI status;
- doctor/readiness;
- provider context injection;
- handoff refresh.

Do not have each UI/packet infer freshness independently.

The packet should carry graph generation/domain/base binding explicitly enough to prove what revision the context came from.

### Acceptance tests

- no applicable invariant with proven coverage -> `empty-proven`;
- uncovered file -> `absent`/`uncovered`, never `empty-proven`;
- graph behind task base -> `stale`/`behind`;
- mismatched repository/domain -> `mismatched`;
- store unavailable -> `unavailable` with cause;
- all rendered surfaces agree because they consume the same normalized state.

---

## P0.8 Make cross-agent continuity an actual runtime feature, not only packet plumbing

### Current state

The context/handoff packet types are useful and digest-bound, but the current `handoff` CLI is manual. It does not yet prove the product requirement that switching providers mid-task automatically carries prior scope, decisions, evidence, and open questions into the next agent session.

The CLI also does not currently capture a decisions list when building the handoff.

### Required redesign

Create a durable task/session model that can assemble handoff state from the current task rather than asking the user to manually restate it.

On provider switch:

```text
refresh Sensei context at current generation
+ bind prior task identity/base
+ include decisions already made
+ include current diff/evidence/tests
+ include unresolved questions
-> deliver to next provider through its supported context/session mechanism
```

Provider session IDs (Claude session, Codex thread/app-server session, etc.) are execution continuity hints, not architectural authority.

### Acceptance tests

- start task with provider A, make progress, switch to provider B;
- provider B receives the exact task identity, refreshed context, prior decisions, current diff/evidence, and open questions;
- no cold-prompt restart;
- stale handoff context is refreshed or explicitly rejected;
- local session loss cannot manufacture missing Sensei authority.

---

# P1: next product-hardening slices

## P1.1 Make `doctor` the single readiness computation promised by the architecture

The current doctor checks executables, provider authentication state, MCP startup, and tool presence. It does not yet establish the full readiness contract.

Add typed checks for:

- repository identity;
- exact HEAD / working-tree state;
- Sensei domain binding;
- graph generation and freshness versus task base;
- store/service availability;
- provenance/version compatibility of Sensei components;
- instance ownership so Sensei Code never stops/reconfigures an instance it did not start;
- per-mode required Sensei tool subset;
- provider role capability/sandbox compatibility.

The TUI must consume the same report object. No second optimistic readiness path.

## P1.2 Split the workflow monolith by semantic ownership

`internal/workflow/engine.go` currently mixes:

- Sensei gates;
- architect parsing;
- authority routing;
- human rendezvous;
- worker lifecycle;
- review lifecycle;
- prompt construction.

Keep one coordinator, but split semantics enough that ownership is testable:

```text
internal/workflow/coordinator.go
internal/workflow/assisted.go
internal/workflow/governed.go
internal/authority/router.go
internal/sensei/contracts.go
internal/execution/broker.go
internal/task/state.go
```

This is a boundary refactor, not a request for framework-heavy abstraction.

## P1.3 Make event/session persistence failures visible

`Engine.emit` currently ignores `Store.Append` errors.

Local session state is not Sensei authority, but continuity depends on knowing whether it is durable. A disk/full/permission failure must surface a session-degraded event or fail the features that require replay rather than silently pretending persistence succeeded.

## P1.4 Add restart/resume around durable task identity

The current session is timestamp-created and cannot resume active work after process restart.

Resume should reconstruct presentation/execution state from local events plus canonical Sensei task state, while treating Sensei as authority whenever local state disagrees.

## P1.5 Strengthen the MCP client boundary

Add:

- required-result schema validation;
- bounded call timeouts/cancellation;
- useful stderr diagnostics without mixing stderr into protocol framing;
- explicit handling of unexpected responses/notifications/server requests as required by the supported MCP contract;
- protocol/server version reporting in readiness.

Do not add general MCP complexity unless Sensei's public contract requires it.

## P1.6 Expand tests around the actual laws

The current tests establish useful foundation behavior, but the next suite should use fake Sensei and fake provider adapters to test complete transitions.

Highest-value tests are the governing ones:

- stale/unavailable/malformed Sensei evidence;
- semantic preflight refusal;
- semantic audit violation;
- certifiability-driven Level-3 routing;
- no model-confidence routing;
- capability-envelope enforcement;
- dirty-base behavior;
- agent handoff continuity;
- assisted/governed vocabulary separation;
- persistence degradation;
- exact candidate/base evidence binding.

Prefer state-machine tests over snapshotting terminal strings.

## P1.7 Keep the TUI thin, then polish it

The current UI direction is good. After the control plane is correct, add the useful operator surfaces already implied by the design:

- visible current mode;
- graph freshness/generation/domain indicator;
- task identity/state;
- compact current plan/scope;
- evidence/diff view;
- provider switch/handoff command;
- slash commands;
- progress indicators for long operations;
- clear degraded/readiness states.

The TUI must never calculate governance truth itself.

## P1.8 Package the runtime as a product

The architecture correctly says users should not need to understand or manually operate the backing graph store.

After readiness semantics are correct, implement the install/runtime ownership model for:

```text
sensei-code
Sensei binaries
owned Sensei/store instance
state directory
provider detection
```

The hard rule is ownership: Sensei Code may manage the instance it started/owns, not arbitrary existing Sensei processes on the machine.

---

# P2: complete the governed lifecycle only through canonical Sensei owners

The existing implementation correctly stops before admission. Keep that fail-closed boundary until the upstream external-candidate contract is settled.

When Sensei core exposes the required public contract, implement:

1. canonical task/convergence binding;
2. externally generated candidate sealing through Sensei's owner;
3. exact candidate identity/digest;
4. `sensei_workspace_admit_change` with the canonical request artifacts;
5. exact admitted artifact application;
6. `sensei_workspace_verify_admission` on the observed target;
7. proof/test/runtime evidence completion;
8. `complete_task` terminal truth;
9. optional local commit/push/PR publication according to the configured human publication policy.

No local substitute for admission, verification, completion, or candidate sealing should be added to Sensei Code.

---

# Proposed implementation sequence

The order matters because later autonomy is only safe when earlier truth boundaries are mechanical.

## Slice A: typed Sensei contract adapters

- remove control dependence on `firstText`;
- validate structured workspace/preflight/audit results;
- centralize absence/freshness normalization;
- add malformed/empty semantic-result tests.

**Exit:** Sensei result semantics can no longer be overridden by prompt interpretation.

## Slice B: certifiability authority router

- architect claims protocol;
- canonical Sensei checks for claims/coverage/contracts/ownership;
- Level-2 grant vs Level-3 routing;
- tests proving model uncertainty has no authority effect.

**Exit:** the human is interrupted only because a computable authority boundary was reached.

## Slice C: assisted-default coordinator

- explicit mode state;
- assisted TUI workflow becomes default;
- governed flow remains opt-in and honestly labeled;
- shared events/session/provider infrastructure.

**Exit:** plain `sensei-code` behaves like the intended Pi/Claude-Code-style adoption surface rather than immediately creating a governed candidate.

## Slice D: mechanical execution capability broker

- capability checks at every executable action;
- provider role capability declaration;
- no-push/no-force-push/canonical-write enforcement;
- environment/sandbox hardening without permission popups.

**Exit:** the configured local authority envelope is a mechanism, not documentation.

## Slice E: durable task continuity and human-resolution learning

- runtime-integrated provider handoff;
- task resume substrate;
- Level-3 answer persisted through canonical Sensei governance write path.

**Exit:** switching agents does not lose the task, and answering a human-owned question makes that class of question disappear on replay.

## Slice F: full readiness/install contract

- one `doctor` computation;
- freshness/domain/instance ownership/tool/provider compatibility;
- TUI uses the same report;
- cold repository onboarding.

**Exit:** a new user can distinguish ready, degraded, uncovered, stale, mismatched, and unavailable without knowing Sensei internals.

## Slice G: canonical governed admission lifecycle

Only after the upstream external-candidate contract is proven.

**Exit:** `governed` means real Sensei receipts and exact bindings exist from candidate through completion.

---

# Suggested invariants to add before implementation

These should be represented in Sensei before or alongside the corresponding code changes.

```text
sensei_code.authority.escalation_is_certifiability_driven
sensei_code.authority.model_confidence_has_no_authority
sensei_code.sensei.required_results_are_typed_and_nonempty
sensei_code.sensei.negative_verdict_cannot_be_model_overridden
sensei_code.mode.assisted_is_default_and_never_claims_governed
sensei_code.execution.capability_envelope_is_mechanical
sensei_code.execution.governed_workers_cannot_mutate_canonical_checkout
sensei_code.candidate.bound_to_exact_base
sensei_code.human_resolution.must_become_governed_knowledge
sensei_code.context.absence_is_semantically_typed
sensei_code.context.handoff_is_runtime_continuity
sensei_code.readiness.single_computation
```

Do not add these merely as documentation. Each invariant should name the tests that prove its enforcement path.

---

# Definition of the next credible working version

The next version is credible when a developer can launch `sensei-code` in a repository and observe all of the following:

1. the TUI opens in **assisted** mode with explicit repository/domain/freshness truth;
2. the selected coding provider works autonomously without routine permission prompts;
3. Sensei context is structured, provenance-bound, and cannot silently turn stale/uncovered evidence into green context;
4. switching providers preserves the same task, decisions, diff, evidence, and open questions;
5. an ordinary implementation failure recovers autonomously;
6. an architectural decision inside the certifiable region is resolved without bothering the human;
7. a genuine authority crossing produces the small 1/2/3 human decision surface;
8. that human resolution becomes durable Sensei-governed knowledge;
9. configured execution capabilities are mechanically enforced;
10. governed mode, when selected, uses isolated exact-base candidates and never claims admission before Sensei actually admits the exact artifact.

At that point Sensei Code will match the original product idea: **a thin, autonomous orchestration surface that uses Sensei at full strength, does not duplicate it, and interrupts the human only when authority genuinely leaves the system.**

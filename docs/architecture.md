# Sensei Code Architecture

**Status:** Initial governing architecture  
**Audience:** Sensei maintainers, Sensei Code implementers, provider-adapter authors, reviewers  
**Product:** `globulario/sensei-code`

---

## 1. Purpose

Sensei Code is the local interactive product surface for governed AI software development with Sensei.

Its purpose is to make the full Sensei workflow usable through an experience comparable to modern conversational coding tools while preserving the authority, evidence, identity, and closure boundaries that make Sensei different from an ordinary agent harness.

The primary experience is:

```text
human + AI architect
    -> understand governed repository state
    -> identify a bounded task
    -> resolve architectural questions
    -> approve a plan
    -> delegate implementation to one or more bounded workers
    -> produce isolated sealed candidates
    -> evaluate evidence and proof obligations
    -> review an exact candidate
    -> request Sensei admission
    -> apply only the exact admitted artifact
    -> verify the exact result
    -> human publication / merge decision
```

Sensei Code replaces the manual relay among ChatGPT/Codex, Claude Code, Codex CLI, terminals, Git worktrees, GitHub, and Sensei commands.

It does **not** replace Sensei.

---

## 2. Product thesis

Traditional AI coding products center an agent session and give that agent increasingly broad access to the repository.

Sensei Code centers a **governed change**.

The conversation is important, but it is not authority. The diff is important, but it is not proof. Passing tests are important, but they are not closure. A model recommendation is useful, but it is not admission.

Sensei Code therefore separates:

1. **reasoning** — architect and reviewer models;
2. **execution** — worker providers;
3. **architectural truth and governance** — Sensei;
4. **repository state** — Git;
5. **collaboration/publication** — optional GitHub;
6. **final publication authority** — the human maintainer or an explicitly configured future owner policy.

The application is successful when this separation feels natural rather than burdensome.

---

## 3. Relationship to Sensei

Sensei is upstream authority.

Sensei Code is a consumer and orchestrator of Sensei-owned contracts and operations.

### 3.1 Sensei owns

Sensei owns the semantic truth of:

- repository and canonical domain identity;
- graph authority, graph generation, and freshness;
- governed knowledge sources;
- architecture graph compilation and projections;
- intents and architectural direction;
- invariants;
- contracts;
- authority domains and legal mutation paths;
- known failure modes;
- forbidden fixes;
- required tests and proof obligations;
- briefing;
- impact analysis;
- preflight and edit checks;
- task/session identity;
- admission;
- capability consumption;
- mutation observation;
- scope verification;
- evidence and certification;
- synthesis session state and budgets;
- sealed candidate artifacts;
- deterministic candidate evaluation;
- governed candidate application;
- admission verification;
- closure assessment;
- completion, revocation, migration, and related receipts.

Sensei Code must not define weaker local substitutes for any of these concepts.

### 3.2 Sensei Code owns

Sensei Code owns:

- interactive terminal UX;
- local application/session lifecycle;
- provider detection and readiness presentation;
- native provider process lifecycle;
- role assignment;
- conversation orchestration;
- exact Git worktree preparation where not already owned by a Sensei operation;
- normalized provider events;
- user approvals;
- task/run presentation;
- optional GitHub publication workflow;
- local resumability for UX state;
- mapping from user intent into calls to canonical Sensei operations.

Sensei Code may cache Sensei results for presentation, but a cache is never a new authority surface.

### 3.3 No hidden architectural database

Sensei Code must not create a private database containing a second copy of project architectural truth.

Persistent project truth belongs in repository-owned Sensei sources and Sensei's append-only governance/closure records.

Sensei Code may store local UX state such as:

- conversation transcript;
- selected provider;
- window/view state;
- pending local prompts;
- process IDs while a session is live;
- normalized event history;
- references to canonical Sensei receipt IDs and digests.

That state must always be distinguishable from authoritative Sensei records.

---

## 4. Relationship to Sensei Dashboard

Sensei Code carries forward the AI workspace architecture explored in `globulario/sensei-dashboard`, but changes the product center.

The Dashboard work established several useful boundaries:

- Sensei core owns architectural truth;
- the local runner owns process and orchestration concerns;
- model providers own their authentication;
- workers are replaceable and bounded;
- governed runs use exact repository/worktree identity;
- GitHub is a durable collaboration ledger, not architectural authority;
- manual and governed sessions must remain structurally distinguishable;
- the human retains final merge authority.

Those principles remain.

The Dashboard's original product center was an architectural observatory with an AI workspace added beside it.

Sensei Code's product center is the interactive governed change workflow itself.

Architecture maps, Focus views, evolution views, risk views, and rich evidence visualization remain valuable future views, but they are downstream projections of Sensei state and are not required for the first useful release.

### 4.1 One important semantic update

The earlier workspace design assumed a mutation-admission gate before starting a worker.

Sensei's later governed synthesis architecture provides a more precise boundary:

```text
provider works in disposable candidate workspace
    -> candidate is sealed
    -> candidate is evaluated
    -> candidate becomes candidate-ready
    -> Sensei admission decides whether that exact artifact may be applied
    -> only the exact admitted artifact is materialized into the governed target
    -> Sensei verifies the result
```

Sensei Code adopts the newer model.

A provider may mutate its disposable candidate workspace without thereby mutating authoritative project state. Admission is required before a sealed candidate can become the governed applied result.

This distinction is foundational.

---

## 5. Authority boundaries

### 5.1 Human maintainer

The human owns:

- genuinely open architectural decisions;
- selection of provider accounts and permissions;
- approval of architecture plans where policy requires it;
- explicit candidate selection when several candidate lanes exist;
- authorization to request admission where policy requires it;
- authorization of publication actions;
- final merge authority unless a later, separately governed policy explicitly delegates it.

The UI must make these boundaries explicit.

### 5.2 Architect runtime

The architect owns procedure, not authority.

The architect may:

- discuss intent with the human;
- request Sensei briefing, impact, resolve, query, and preflight information;
- inspect Git history and repository state;
- identify architectural questions;
- propose a bounded interpretation and plan;
- identify expected scope and proof obligations;
- review candidate and exact-result evidence;
- recommend accept, revise, reject, or architectural-question outcomes.

The architect may not:

- silently change Sensei governance sources;
- fabricate an admission state;
- silently enlarge task scope;
- treat model memory as project authority;
- silently select a winning candidate when multiple candidates exist;
- commit, push, merge, or publish without an explicit authorized action;
- rewrite a governing plan during implementation without a visible amendment path.

### 5.3 Worker provider

A worker owns bounded execution.

A worker may:

- inspect the candidate workspace;
- use the Sensei tools intentionally exposed to the run;
- edit files inside its bounded candidate workspace;
- run allowed commands;
- execute required tests;
- produce structured output and observations.

A worker may not:

- own or redefine the architectural contract it implements;
- enlarge its own scope;
- decide admission;
- apply its work into the authoritative target directly;
- replay changes after a sealed artifact has been admitted;
- claim test or evidence success without observable evidence;
- commit, push, merge, or publish unless a later explicit capability and policy authorizes a specific action.

### 5.4 Reviewer runtime

The reviewer owns a recommendation only.

The reviewer operates on exact bound inputs:

- task identity;
- governing architectural context;
- base identity;
- exact candidate/result identity;
- diff/change evidence;
- test/evaluator evidence;
- Sensei findings;
- unresolved unknowns;
- proof requirements and receipts.

Its recommendation cannot substitute for Sensei admission or human publication authority.

### 5.5 Sensei

Sensei owns governance truth.

Sensei Code must fail closed when the relevant Sensei operation reports stale, conflicting, malformed, refused, blocked, uncertifiable, incomplete, or unavailable state.

The UI may explain a failure. It may not reinterpret it into success.

### 5.6 Git and GitHub

Git owns repository object identity and history mechanics.

GitHub optionally owns collaboration records such as issues, PRs, CI runs, comments, and remote merge history.

Neither Git nor GitHub independently determines whether a change is architecturally valid.

---

## 6. High-level architecture

```text
+------------------------------------------------------------------+
|                         Sensei Code UI                            |
|                                                                  |
|  conversation  task status  agents  diff  evidence  approvals   |
+-------------------------------+----------------------------------+
                                |
                                v
+------------------------------------------------------------------+
|                     Application Orchestrator                     |
|                                                                  |
|  session state machine                                           |
|  role assignment                                                 |
|  workflow policy                                                 |
|  approval boundaries                                             |
|  event normalization                                             |
|  cancellation / resume                                           |
+------+----------------+----------------+---------------+----------+
       |                |                |               |
       v                v                v               v
+-------------+  +-------------+  +-------------+  +--------------+
| Sensei      |  | Architect   |  | Worker      |  | Git / GitHub |
| Adapter     |  | Runtime     |  | Providers   |  | Gateways     |
+------+------+  +------+------+  +------+------+  +------+-------+
       |                |                |                |
       v                v                v                v
   sensei CLI       Codex app-       Claude Code         git
   MCP / typed      server or        Codex CLI           gh / API
   contracts        future runtime   future providers
       |
       v
+------------------------------------------------------------------+
|                         Sensei Core                              |
|                                                                  |
| graph | task | synthesis | evaluation | admission | apply |      |
| evidence | proof | closure | receipts | provenance               |
+------------------------------------------------------------------+
```

The application orchestrator coordinates the workflow but does not own the semantic meaning of Sensei decisions.

---

## 7. Implementation language and process model

Sensei Code should initially be implemented as a single Go application.

Reasons:

- Sensei itself is Go;
- the target product is a local CLI/TUI;
- Go provides strong process, signal, filesystem, and concurrency primitives;
- a single binary simplifies adoption;
- provider runtimes are already external processes;
- Git and Sensei can be invoked through direct argv without shell interpolation.

The architecture must not require linking Sensei internals directly into the Sensei Code binary.

The preferred boundary is through **public Sensei interfaces**:

- stable CLI commands with structured output;
- canonical versioned contracts;
- MCP where an agent needs Sensei tools;
- gRPC only where Sensei explicitly exposes a stable public runtime contract.

This keeps Sensei Code upgradeable independently from Sensei's internal Go package layout.

---

## 8. Proposed repository layout

```text
cmd/
  sensei-code/
    main.go

internal/
  app/
    app.go
    commands.go
    lifecycle.go
    approvals.go

  session/
    session.go
    store.go
    resume.go

  protocol/
    event.go
    message.go
    capability.go

  sensei/
    client.go
    readiness.go
    task.go
    synthesis.go
    admission.go
    verification.go
    evidence.go

  architect/
    runtime.go
    codexapp/

  provider/
    provider.go
    claude/
    codex/
    cursor/

  review/
    reviewer.go
    packet.go

  git/
    repository.go
    identity.go
    diff.go

  worktree/
    manager.go
    lease.go

  github/
    gateway.go

  ui/
    model.go
    views.go
    input.go
    render.go

  process/
    runner.go
    processgroup.go
    limits.go

  config/
    config.go
    policy.go

  testkit/
    fake_provider.go
    fake_sensei.go
```

Provider-specific packages depend inward on shared provider interfaces. Core app state must not import vendor SDK types.

---

## 9. Local state model

Sensei Code needs resumability without creating a shadow authority database.

### 9.1 Repository-owned state

Repository-owned state should remain minimal.

Possible tracked configuration:

```text
.sensei-code.yaml
```

It may contain non-secret preferences such as:

- default architect provider;
- preferred worker providers;
- allowed worker capabilities;
- whether parallel candidate lanes are enabled;
- approval policy defaults;
- GitHub publication defaults;
- UI preferences that are intentionally project-wide.

It must not contain provider tokens.

Architecture, invariants, contracts, evidence rules, admission policy, and task closure truth remain Sensei-owned.

### 9.2 User-local state

Resumable UX state should live outside the repository, for example under the platform-appropriate user state directory:

```text
$XDG_STATE_HOME/sensei-code/
~/Library/Application Support/sensei-code/
%LOCALAPPDATA%\sensei-code\
```

A repository is keyed by stable repository identity, not only by pathname.

Local state may contain:

- UI session IDs;
- transcript/event log;
- provider thread IDs exposed by the provider;
- local worktree leases;
- references to Sensei task/admission/receipt IDs;
- selected task and run IDs;
- timestamps and last view state.

It must be safe to delete this state without erasing authoritative project history.

### 9.3 Transcript status

Conversation transcripts are useful memory but are not architectural authority.

When an architectural conclusion must survive a session, the workflow must convert it into an appropriate repository-owned or Sensei-owned governed artifact rather than relying on hidden transcript memory.

---

## 10. Repository opening and onboarding

Sensei Code must make Sensei adoption easier, including repositories that are not yet ready for governed synthesis.

Opening a repository runs a readiness sequence.

```text
open repository
  -> resolve Git identity and HEAD
  -> detect Sensei executable
  -> inspect Sensei initialization
  -> verify workspace identity
  -> verify graph authority/freshness
  -> verify coverage/readiness
  -> detect configured provider runtimes
  -> present readiness state
```

Possible states include:

```text
ready
degraded
needs-sensei-init
needs-bootstrap
needs-graph-build
needs-provider-login
stale
mismatched
unavailable
```

Sensei Code must not silently bootstrap or mutate governance sources merely to reach green status.

Instead it guides the user through explicit onboarding steps such as:

```text
sensei init --mcp
sensei bootstrap --repo .
sensei serve
sensei build
sensei repo-eval
```

The exact commands remain owned by Sensei and may evolve. Sensei Code should prefer machine-readable readiness interfaces when available rather than hardcoding terminal text parsing.

This onboarding layer directly addresses the current synthesis precondition that a repository must already have real Sensei graph/task/closure state before the governed synthesis driver can run.

Sensei Code must solve that as an adoption workflow, not by fabricating placeholder bindings.

---

## 11. Interactive UX model

Sensei Code is conversation-first but state-machine-backed.

The main screen should combine:

- conversation;
- current task;
- repository/base identity;
- Sensei readiness;
- current workflow phase;
- active agent/provider;
- pending approval;
- compact evidence/findings summary.

Detailed views can be opened for:

- Sensei briefing;
- architecture/impact;
- worker activity;
- diff;
- tests;
- evidence;
- admission;
- verification;
- receipts;
- GitHub/CI.

### 11.1 Natural language first

A user should be able to type:

```text
Fix the cluster packaging problem we saw on Dell.
```

The application interprets this as a request to begin or continue a governed task workflow.

Slash commands exist for precise control, not as the only usable interface.

### 11.2 Planned control commands

```text
/help
/new
/resume
/task
/status
/sensei
/briefing
/impact
/plan
/agents
/run
/cancel
/diff
/tests
/evidence
/review
/admit
/apply
/verify
/pr
/quit
```

Commands that can change authoritative state require explicit presentation of what will happen before execution.

---

## 12. Workflow state machine

The UI must not infer workflow phase from prose. It uses typed state.

An initial product-level state machine can be:

```text
RepositoryReady
    |
    v
TaskOpened
    |
    v
ContextLoaded
    |
    v
ArchitectureProposed
    |
    +----> ArchitecturalQuestion
    |             |
    |             `----> ContextLoaded
    |
    v
ArchitectureApproved
    |
    v
CandidateRunning
    |
    +----> CandidateFailed
    |
    v
CandidateReady
    |
    +----> ReviewRevise ----> CandidateRunning / ArchitectureProposed
    +----> ReviewReject ----> terminal or new task
    |
    v
CandidateSelected
    |
    v
AdmissionRequested
    |
    +----> Waiting
    +----> Refused
    +----> Uncertifiable
    |
    v
Admitted
    |
    v
Applied
    |
    v
Verified
    |
    v
ExactResultReviewed
    |
    v
ReadyForPublication
    |
    +----> LocalComplete
    +----> DraftPRCreated
    +----> Published
```

This product state does not replace Sensei's synthesis/task/admission state machines. It references and presents them.

Where Sensei already owns a state transition, the Sensei record is canonical.

---

## 13. Architect integration

### 13.1 Default runtime

The first primary architect target is OpenAI through Codex's programmatic local runtime, preferably `codex app-server` when its protocol provides the required thread, streaming, approval, and tool behavior.

Authentication remains owned by the native OpenAI runtime.

Sensei Code stores provider readiness and provider thread references only.

### 13.2 Architect context packet

The architect should receive a structured context packet assembled from canonical sources:

```text
User intent
Repository identity
Base revision
Sensei workspace identity
Sensei graph authority/freshness
Task identity
Task briefing
Applicable invariants
Contracts
Failure modes
Forbidden fixes
Known architectural decisions
Impact results
Required tests
Proof obligations
Unresolved / contested knowledge
Relevant Git history / GitHub context
```

The packet must preserve unknown and unavailable states.

Absence is never rendered as "none" unless Sensei actually proves the relevant set empty.

### 13.3 Architect output

The architecture proposal should be structured enough to bind later stages:

```text
problem statement
observed evidence
intended behavior
Sensei identities referenced
proposed scope
out-of-scope items
preserved contracts/invariants
required architectural changes, if any
required tests
required proof
implementation constraints
open questions
stop conditions
```

A prose explanation may accompany this structure, but the workflow must not depend only on free-form text.

### 13.4 Architectural amendments

If implementation discovers that the plan is insufficient or wrong, the worker returns an architectural question.

The architect may then propose an amendment.

An amendment creates a new explicit version/binding. It must not silently mutate the plan under an already-running governed candidate.

---

## 14. Worker provider model

The worker abstraction must acknowledge real capability differences among providers.

A capability record may include:

```text
interactive_auth
headless_execution
streaming_output
session_resume
mcp
skills
sandboxing
command_approval
file_approval
structured_output
working_directory_control
timeouts
process_group_control
```

The common interface must not pretend that every provider supports every feature.

### 14.1 Initial workers

Initial first-class providers:

1. Claude Code
2. Codex CLI

Later:

- Cursor Agent;
- other local provider runtimes that can satisfy the execution contract.

### 14.2 Native authentication

Sensei Code never asks users to paste provider API keys merely to use a provider whose native CLI already owns a subscription login.

Provider login is initiated and verified through the native runtime.

Sensei Code must avoid copying broad ambient environment variables into provider processes. Environment propagation is allowlisted.

### 14.3 Execution boundary

Commands are spawned using direct argv.

No shell interpolation.

The process layer owns:

- cancellation;
- deadlines;
- process-group termination;
- stdout/stderr limits;
- structured-output limits;
- working-directory binding;
- environment allowlists;
- bounded event capture.

Provider stderr is observation, not authority.

---

## 15. Sensei synthesis integration

Sensei Code must reuse Sensei's governed synthesis stack whenever the requested workflow reaches candidate generation.

The canonical conceptual chain is:

```text
O1 synthesis session
  -> O2 provider request/result
  -> O3 isolated generation and CandidateArtifact
  -> O4 deterministic evaluation
  -> bounded retry / replan
  -> candidate-ready receipt
  -> O5 admission composition
  -> O5B exact candidate apply
  -> admission verification
  -> completion evidence
```

Sensei Code presents this chain and supplies the human/provider interaction around it.

It must not:

- invent its own candidate artifact format;
- reimplement admission evaluation;
- copy the provider's edits manually after admission;
- let a provider replay an admitted patch;
- select a different artifact after admission;
- treat O4 acceptance as admission;
- treat admission as completion;
- treat successful apply as closure.

### 15.1 `sensei synthesis-run`

Where the Sensei CLI provides a stable `synthesis-run` surface, Sensei Code may invoke it as the first integration point.

The application should prefer structured receipts/events over parsing human-oriented terminal output.

If Sensei later exposes a stable library-neutral RPC for the same driver, the adapter may change without changing Sensei Code's product semantics.

---

## 16. Multi-agent candidate lanes

Sensei Code should support more than one implementation provider without inventing model consensus as authority.

### 16.1 Independent lanes

Each worker lane starts from the same declared base identity unless an explicit architecture amendment says otherwise.

```text
Task T / base B
      |
      +--> lane A / Claude --> candidate A / evidence A
      |
      `--> lane B / Codex  --> candidate B / evidence B
```

Each lane has independent:

- run identity;
- provider identity;
- candidate artifact digest;
- evaluator evidence;
- findings;
- terminal status.

### 16.2 Candidate selection

A candidate is never selected merely because:

- more agents voted for it;
- it is smaller;
- its model reports higher confidence;
- it finished first;
- it passed a subset of tests another candidate did not run.

Selection is an explicit decision informed by the architect/reviewer and Sensei evidence.

The selected candidate's exact artifact digest becomes the only candidate eligible for the following admission request.

### 16.3 Safe first release

Parallel implementation is not required for v1.

The first complete workflow may support one selected worker per task while keeping the data model capable of representing multiple independent lanes later.

---

## 17. Git and worktree model

Exact repository identity is mandatory.

### 17.1 Read-only architect context

The architect may inspect the user's current checkout and Git history.

The application records the exact base revision used to construct a task.

If HEAD changes materially before candidate generation, Sensei Code must surface drift and require re-binding/replanning rather than silently continuing.

### 17.2 Worker isolation

Workers operate in disposable candidate workspaces bound to the exact base.

Where Sensei's synthesis stack already owns candidate workspace creation, Sensei Code delegates to it.

Sensei Code's own worktree manager is used only for workflow needs not already owned by Sensei, for example:

- an exact-SHA reviewer checkout;
- a publication staging worktree;
- provider-specific isolation outside the synthesis path.

### 17.3 Dirty target

A governed apply target must satisfy Sensei's target cleanliness and base-binding rules.

Sensei Code must never "helpfully" stash, overwrite, or merge unrelated local modifications to get past this check without explicit user action.

---

## 18. Review model

Review has two distinct moments.

### 18.1 Candidate review

Before requesting admission, the reviewer evaluates the sealed candidate.

The review packet includes:

- architecture plan/version;
- task identity;
- exact base;
- candidate artifact identity;
- exact diff/change evidence;
- evaluator receipt;
- relevant Sensei impact/findings;
- tests and proof evidence;
- remaining unknowns.

Possible recommendation:

```text
ACCEPT_CANDIDATE
REVISE_CANDIDATE
REJECT_CANDIDATE
ARCHITECTURAL_QUESTION
```

### 18.2 Exact-result review

After admitted application and Sensei verification, the reviewer may review the exact applied result.

This guards against the category error of reviewing one candidate and later publishing a different tree.

The exact-result review binds the verified result identity.

### 18.3 Reviewer independence

The initial product may reuse the primary OpenAI architect in a fresh reviewer thread, but the protocol must distinguish architect and reviewer roles and bind the review to exact inputs.

A future policy may require a separate provider or model for independent review.

---

## 19. Admission and apply UX

Admission is a visible boundary.

The UI should present a compact packet before requesting it:

```text
Task:            T
Base:            B
Candidate:       C
Architecture:    P
Evaluation:      passed / findings
Reviewer:        ACCEPT_CANDIDATE
Required proof:  represented / missing
Unknowns:        none / explicit list
```

Then:

```text
Request Sensei admission for candidate C? [y/N]
```

Sensei Code sends the request to the canonical Sensei owner and displays the unchanged outcome.

Possible outcomes include:

```text
Admitted
AdmittedWithConditions
Waiting
Refused
Uncertifiable
```

Sensei Code must preserve conditions and refusal detail.

### 19.1 Apply

If admitted, only the exact admitted artifact may be applied.

The provider is not asked to repeat its edits.

After apply, Sensei independently recomputes and verifies the result according to the canonical apply/verification contracts.

---

## 20. Evidence and closure presentation

Sensei Code should make proof legible without reducing it to one misleading score.

A compact evidence view may group:

```text
Identity
Scope
Direction
Authority
Mutation
Protection
Epistemic state
Proof
Freshness
Completion
```

Each dimension can be:

```text
satisfied
blocked
unknown
not-applicable
stale
conflicting
unavailable
```

The exact vocabulary should come from canonical Sensei contracts where available.

The UI must never synthesize a global green state when one load-bearing dimension remains unknown or unsupported.

### 20.1 Claims vs receipts

Required proof and observed evidence remain different objects.

The UI must distinguish:

```text
required: TestFoo must pass
```

from:

```text
observed: TestFoo executed against result R and passed
```

A requirement is not a receipt.

---

## 21. GitHub integration

GitHub is optional and downstream of local correctness.

### 21.1 Responsibilities

The GitHub gateway may manage:

- issue binding;
- draft PR creation;
- push;
- CI observation;
- PR comments/reviews;
- exact head SHA;
- merge status.

### 21.2 Authentication

Prefer the user's existing `gh`/Git authentication or an explicit GitHub authentication flow.

Sensei Code should not become a general-purpose credential store.

### 21.3 Publication boundary

No provider receives implicit permission to push or open a PR.

Publication is an explicit workflow action:

```text
Verified local result
    -> human approves publication
    -> commit / push
    -> draft PR
    -> CI observation
    -> exact-SHA review
    -> human merge
```

A future policy may automate selected steps, but the authority must be explicit and auditable.

---

## 22. Manual vs governed sessions

Sensei Code must preserve the distinction established by the earlier workspace contracts.

### 22.1 Manual session

A manual session may use:

- a provider CLI;
- Sensei MCP;
- generated `CLAUDE.md` / `AGENTS.md` / Cursor rules;
- briefing and preflight;
- normal Git edits.

This can be useful for exploration, review, and low-risk work.

It is not represented as a governed run unless the required canonical task, candidate, admission, verification, and receipt chain exists.

### 22.2 Governed session

A governed session has real canonical references and receipts.

At minimum its UI record must be able to point to the authoritative Sensei objects proving the relevant lifecycle.

The application must make it impossible for a manual session record to masquerade as a governed one merely by filling a boolean.

---

## 23. Provider event protocol

The UI needs one normalized event stream across different provider runtimes.

A first internal event family may include:

```text
session.started
session.resumed
provider.ready
provider.auth_required
provider.started
provider.output
provider.tool_started
provider.tool_finished
provider.approval_required
provider.failed
provider.completed
sensei.operation_started
sensei.operation_finished
sensei.blocked
candidate.sealed
candidate.evaluated
review.completed
admission.decided
apply.completed
verification.completed
github.action_started
github.action_finished
workflow.approval_required
workflow.completed
```

Every event includes stable identifiers appropriate to its layer.

The event stream is presentation/history, not a substitute for canonical Sensei receipts.

---

## 24. Failure semantics

Failure classes must remain distinct.

Do not flatten everything into `FAILED`.

Important categories include:

```text
repository_drift
sensei_unavailable
sensei_stale
sensei_identity_mismatch
provider_not_installed
provider_auth_required
provider_crash
provider_timeout
provider_invalid_output
user_cancelled
architecture_unresolved
candidate_rejected
candidate_invalid
candidate_tampered
evaluator_unavailable
evaluator_rejected
admission_waiting
admission_refused
admission_uncertifiable
apply_base_drift
apply_dirty_target
apply_digest_mismatch
verification_failed
proof_incomplete
ci_failed
publication_failed
```

A retry policy must be explicit per class.

Infrastructure flakiness must not be silently converted into a model or architecture failure, and vice versa.

---

## 25. Security model

Sensei Code coordinates powerful local tools and therefore needs a narrow trust boundary.

### 25.1 No shell interpolation

All managed commands use direct executable + argv invocation.

### 25.2 Environment allowlist

Provider processes receive only explicitly allowed environment variables plus required platform basics.

Broad ambient credentials must not be copied blindly.

### 25.3 Working-directory confinement

Each managed provider process is bound to an intended workspace.

### 25.4 Process-group cancellation

Cancellation and timeouts terminate the entire spawned process group, not only the parent CLI.

### 25.5 Output limits

Stdout, stderr, structured result, and event counts are bounded.

### 25.6 Credential ownership

Native provider runtimes own provider credentials whenever possible.

Sensei Code stores readiness, not secrets.

### 25.7 No webview shell authority

If a graphical UI is later added, arbitrary shell execution remains in the trusted local backend. A UI/webview never receives raw unrestricted shell capability.

---

## 26. Configuration model

Configuration should be layered:

```text
built-in safe defaults
    < user config
    < repository non-secret config
    < explicit command/session overrides
```

Configuration cannot override Sensei governance truth.

For example, Sensei Code may configure:

```text
default worker = claude
parallel lanes = false
review provider = openai
auto-run non-mutating tests = true
```

It may not configure:

```text
ignore admission refusal = true
assume missing evidence passed = true
rewrite Sensei closure state = true
```

Those are not UI preferences. They would violate ownership boundaries.

---

## 27. Testing strategy

Sensei Code needs deterministic tests despite orchestrating nondeterministic providers.

### 27.1 Fake provider

A deterministic helper provider must support:

- normal completion;
- streamed output;
- malformed output;
- timeout;
- crash;
- cancellation;
- oversized output;
- architectural question;
- retryable failure.

CI must not require external provider credentials.

### 27.2 Fake Sensei adapter

Unit tests may use typed fake Sensei responses for orchestration behavior.

They must not redefine canonical Sensei semantics.

Integration tests against a real Sensei binary should verify contract compatibility.

### 27.3 Git fixtures

Tests should construct real temporary Git repositories/worktrees to prove:

- exact base binding;
- drift detection;
- dirty target handling;
- worktree isolation;
- exact diff identity;
- cleanup and resume behavior.

### 27.4 End-to-end tests

Before a workflow is called governed, an end-to-end test must exercise the real Sensei path appropriate to that workflow.

A UI test that paints `Admitted` is not proof that admission works.

---

## 28. Observability and receipts

Sensei Code should keep a local append-only UX event log per session.

Every event references canonical identities where available.

The application should be able to export a support bundle containing non-secret local execution context such as:

- Sensei Code version;
- Sensei version;
- provider versions;
- repository identity;
- session event log;
- referenced canonical receipt IDs/digests;
- bounded stdout/stderr diagnostics;
- Git status and exact SHAs.

The bundle must distinguish copied observations from canonical source records.

---

## 29. Version compatibility

Sensei Code depends on Sensei contracts, not undocumented implementation behavior.

On startup it should negotiate or verify:

- Sensei version;
- required contract versions;
- required CLI/RPC capabilities;
- provider capabilities.

If an incompatible Sensei version is installed, the application reports the incompatibility rather than guessing.

The compatibility check is fail-closed for governed workflows and may be advisory for read-only exploration.

---

## 30. Initial milestones

### M0 — Governing architecture

Deliverables:

- `README.md`;
- this architecture;
- first contract list;
- first implementation brief.

Exit condition:

The ownership boundaries and first vertical slice are reviewable before application code begins.

### M1 — Repository shell and readiness

Deliverables:

- Go binary and interactive shell;
- repository detection;
- Git identity;
- Sensei detection/readiness;
- provider detection/readiness;
- local session/event storage;
- onboarding guidance.

Exit condition:

A user can open a repository and receive a truthful readiness picture without mutation.

### M2 — OpenAI architect vertical slice

Deliverables:

- architect runtime interface;
- Codex app-server adapter;
- native login/readiness flow;
- Sensei briefing/impact/preflight context packet;
- structured architecture proposal;
- explicit human approval.

Exit condition:

A human and architect can produce a bounded plan grounded in real Sensei state.

### M3 — One worker vertical slice

Start with Claude Code or Codex, not both simultaneously.

Deliverables:

- provider interface;
- one real provider adapter;
- bounded process runner;
- isolated exact-base candidate execution;
- normalized streaming events;
- cancellation;
- deterministic fake-provider tests.

Exit condition:

A worker can produce a candidate without mutating the authoritative checkout.

### M4 — Sensei governed synthesis

Deliverables:

- integration with Sensei synthesis/evaluation interfaces;
- candidate artifact/receipt presentation;
- retry/replan state presentation;
- truthful failure classes.

Exit condition:

A real task can reach canonical `candidate-ready` through Sensei machinery.

### M5 — Review, admission, apply, verification

Deliverables:

- exact candidate review packet;
- reviewer recommendation;
- explicit admission action;
- canonical admission outcome rendering;
- exact artifact apply;
- verification;
- exact-result review.

Exit condition:

At least one real repository task completes the actual Sensei admission/apply/verification path without bypass or replay.

### M6 — Second worker + lane model

Deliverables:

- second real provider adapter;
- independent candidate lanes;
- explicit candidate selection;
- lane evidence comparison.

Exit condition:

Two providers can independently produce bound candidates from one base without creating implicit consensus authority.

### M7 — GitHub workflow

Deliverables:

- GitHub readiness;
- issue/PR binding;
- commit/push/draft PR;
- CI observation;
- exact-SHA review;
- publication receipts/references.

Exit condition:

A verified local result can become a draft PR through an explicit publication boundary.

### M8 — Product polish

Deliverables:

- full-screen terminal UI;
- strong session resume;
- richer Sensei views;
- architecture/evidence navigation;
- provider capability management;
- installation/release packaging.

Exit condition:

The workflow feels like a coherent coding product rather than an orchestration demo.

---

## 31. First vertical slice

The first implementation should remain deliberately narrow:

```text
local Git repository
  + real Sensei-ready repository
  + OpenAI architect
  + one worker provider
  + one bounded task
  + isolated candidate
  + real Sensei evaluation
  + reviewer recommendation
  + no automatic publication
```

Do not begin by implementing:

- multiple parallel workers;
- a GUI;
- distributed execution;
- Globular integration;
- a custom architecture database;
- autonomous merge;
- provider credential storage;
- a replacement for Sensei synthesis.

Prove the core loop first.

---

## 32. Architectural laws

The following laws govern Sensei Code unless explicitly superseded by a later reviewed architecture decision.

### Law A — Sensei owns governance truth

Sensei Code consumes Sensei identity, architecture, admission, evidence, and closure contracts. It does not originate competing truth for them.

### Law B — Provider authentication does not create authority

A successfully authenticated model is merely available to perform its configured role.

### Law C — Conversation is not authority

Architect and worker transcript state may guide reasoning but cannot silently become durable architectural truth.

### Law D — Candidate work is not authoritative mutation

A provider may work in an isolated candidate workspace. The candidate becomes eligible for authoritative application only through the real Sensei admission path.

### Law E — Admission binds an exact artifact

Once an artifact is admitted, the provider is never asked to replay or recreate the change. Only the exact admitted artifact may be applied.

### Law F — Manual and governed are different states

A manual session must never be represented as a governed run without real canonical governance references and receipts.

### Law G — Unknown does not become PASS

Missing, stale, conflicting, malformed, or unavailable state remains visible and cannot be normalized into success.

### Law H — Multi-agent agreement is not authority

Two or ten models agreeing does not create architectural or admission authority. Candidate selection is explicit and Sensei governance still applies.

### Law I — Review binds an exact result

A review of candidate/result X cannot authorize publication of result Y.

### Law J — Publication is downstream

Commit, push, PR, merge, and promotion occur only after the relevant local governed workflow and explicit publication policy allow them.

### Law K — Local state is disposable

Deleting Sensei Code's local UX state must not erase authoritative architecture or governance history.

### Law L — The UI never outranks the protocol

A green badge, model summary, or optimistic UI state cannot override the underlying canonical Sensei outcome.

---

## 33. Final product shape

The intended end state is not a dashboard that users occasionally inspect.

It is a coding environment they can live in:

```text
$ sensei-code

> investigate this warning
> explain the governing architecture
> create a task
> ask Claude to implement the approved plan
> compare it with a Codex candidate
> show me the evidence
> review the selected candidate
> request admission
> apply and verify it
> open the draft PR
```

Underneath those natural requests, every important transition remains bound to explicit identity, authority, evidence, and receipts.

That is the adoption goal:

> **Make governed AI development feel as easy to use as an ordinary coding agent without reducing governance to an agent prompt.**

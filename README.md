# Sensei Code

**Governed multi-agent software development powered by Sensei.**

Sensei Code is a local-first interactive development environment for working with AI coding agents through the full Sensei governance model.

The experience should feel familiar to users of Claude Code, Codex, Cursor Agent, Pi, and other conversational coding tools: open a repository, describe what you want, discuss the change, watch agents work, inspect the result, and continue the conversation.

The difference is what sits underneath the conversation.

Sensei Code does not ask one model to be architect, implementor, reviewer, authority, and historian at the same time. It coordinates specialized agents around **Sensei**, which owns the architectural truth, task bindings, authority, admission, evidence, and closure rules for the repository.

```text
human
  |
  v
Sensei Code
  |
  +--> Sensei ---------------- architecture, authority, proof, closure
  |
  +--> OpenAI architect ------- understanding, planning, review
  |
  +--> Claude Code worker ----- bounded implementation
  |
  +--> Codex worker ----------- bounded implementation
  |
  +--> other agents ----------- replaceable execution providers
  |
  +--> Git -------------------- exact repository history and worktrees
  |
  `--> GitHub (optional) ------ issues, PRs, CI, collaboration
```

If **Sensei is the constitution and closure engine**, Sensei Code is the place where people and agents actually work through it.

> Sensei Code makes Sensei easier to adopt without making Sensei easier to bypass.

---

## Why Sensei Code exists

AI coding tools have become excellent interactive implementors. They can inspect repositories, edit files, run tests, and explain their work.

What they do not automatically provide is a shared, repository-owned answer to questions such as:

- What architectural rules apply to this task?
- Which source is authoritative?
- Which failure modes and forbidden fixes are already known?
- Which files and components may legally change?
- Which tests and proof obligations are required?
- Was the implementation produced from the exact repository state that was reviewed?
- Did the observed result match the admitted operation?
- Is the evidence fresh and bound to the exact result?
- May this result become part of the project's authoritative history?

Sensei already provides those structures.

The remaining adoption problem is workflow. Using an AI architect, Claude Code, Codex, Git, GitHub, Sensei briefings, worktrees, gates, admission, evidence, and review manually means moving information among several terminals and conversations.

Sensei Code turns that relay race into one local interactive workspace.

---

## Product thesis

Traditional AI coding tools center the **agent session**.

Sensei Code centers the **governed software change**.

```text
human + architect
      |
      v
understand governed repository state
      |
      v
create a bounded task and architecture plan
      |
      v
produce an isolated candidate with a worker agent
      |
      v
evaluate candidate and collect evidence
      |
      v
review the exact candidate
      |
      v
Sensei admission
      |
      v
apply the exact admitted artifact
      |
      v
verify the exact result and proof obligations
      |
      v
human commit / PR / merge decision
```

The model can reason. The worker can implement. The reviewer can recommend. **Sensei determines what is governed and provable. The human retains final authority over publication and merge.**

---

## What the experience should feel like

The end product is a terminal-first interactive application:

```text
$ sensei-code

  Sensei Code
  repo: github.com/globulario/Globular
  head: 920077c4
  sensei: ready / graph fresh / governed

> Fix the release packaging problem where a bundled dependency is tied to
  the build host's systemd patch version.

Sensei
  Loaded repository identity and task context.
  8 applicable invariants
  3 known failure modes
  2 required proof obligations
  impact: release pipeline, packaging, installer portability

Architect
  I want to separate release inputs from host package discovery...
  Proposed scope: ...
  Required evidence: ...

Approve architecture? [y/N]

Worker: Claude Code
  creating isolated candidate workspace...
  implementing...
  tests: PASS

Sensei evaluation
  candidate sealed
  gate: PASS
  closure requirements: 5/5 represented

Reviewer
  Recommendation: ACCEPT CANDIDATE
  No architectural amendment required.

Request Sensei admission? [y/N]
```

The user should be able to stay in that environment for the whole task instead of manually relaying prompts, diffs, SHAs, and test output between tools.

---

## Sensei is not a plugin

Sensei Code is intentionally built **around the full Sensei model**.

Sensei owns:

- repository and domain identity
- graph authority and freshness
- governed architectural knowledge
- intents, invariants, contracts, failure modes, and forbidden fixes
- briefing and impact analysis
- preflight and edit checks
- task/session bindings
- authority and admission
- synthesis state and budgets
- sealed candidate artifacts
- deterministic evaluation
- evidence and proof obligations
- governed candidate application
- admission verification
- closure and completion truth
- immutable receipts and provenance

Sensei Code must never reproduce a weaker private version of those concepts in its own database.

When Sensei says a fact is unknown, stale, refused, uncertifiable, or incomplete, Sensei Code surfaces that state. It does not turn absence of evidence into a green checkmark.

---

## Agent roles

### Architect

The default primary architect is an OpenAI runtime authenticated through the user's existing ChatGPT/Codex login.

The architect:

- discusses the task with the human
- reads the repository through Sensei and Git
- investigates relevant history
- identifies architectural questions
- proposes a bounded plan
- identifies contracts, invariants, scope, risks, and proof obligations
- reviews worker results
- may request bounded corrections

The architect does **not** gain admission, merge, or architectural authority merely because it produced the plan.

### Workers

Workers are replaceable implementation engines.

Initial targets:

- Claude Code
- Codex CLI

Later adapters may support Cursor Agent and other capable local agent runtimes.

A worker:

- operates inside an isolated, exact-base candidate workspace
- receives the admitted/bounded task context and Sensei access appropriate to the run
- implements the requested change
- runs required commands and tests
- returns evidence and a candidate result

A worker does not own the architecture it implements.

### Reviewer

The reviewer receives the governing task, Sensei context, exact candidate/result identity, diff, evidence, tests, and findings.

Its output is a recommendation such as:

```text
ACCEPT
REVISE
REJECT
ARCHITECTURAL_QUESTION
```

A reviewer recommendation is evidence for the human workflow. It is not Sensei admission and it is not merge authority.

### Human maintainer

The human remains responsible for genuinely open architectural decisions and final publication authority.

Sensei Code should make approvals explicit rather than burying them inside conversational prose.

---

## Local first

Sensei Code must work with a local Git repository without requiring GitHub.

The local core consists of:

```text
Sensei Code
Sensei
Git
native provider CLIs
compiler / tests / local tooling
```

GitHub is an optional collaboration and publication surface for:

- issues
- pull requests
- CI
- review discussions
- remote backup
- collaboration and merge history

GitHub is not the source of architectural truth. Sensei is not replaced when GitHub is unavailable.

---

## Native subscription authentication

Sensei Code should not become a credential vault for AI providers.

Provider runtimes keep ownership of their own authentication:

```text
OpenAI     -> Codex / Codex app-server login
Anthropic  -> Claude Code login
Cursor     -> Cursor Agent login, when supported
GitHub     -> git / gh authentication
```

Sensei Code records only the minimum readiness information it needs, such as installed, authenticated, ready, expired, unavailable, and supported capabilities.

Tokens and provider secrets remain with the native provider runtime whenever possible.

---

## Exact work, not conversational claims

A governed run is bound to exact identities.

At minimum, Sensei Code must preserve and surface:

- repository identity
- base revision
- worktree/candidate identity
- task identity
- Sensei graph/generation identity
- provider and role
- candidate artifact digest
- evaluation receipt
- admission decision
- applied result identity
- verification and evidence receipts

The application must never report that a manual editor or agent session was governed merely because Sensei happened to be installed in the repository.

Manual sessions remain useful for exploration and low-risk work. Governed sessions require the real Sensei bindings and receipts.

---

## Reuse Sensei's synthesis engine

Sensei already contains the governed synthesis machinery required for the dangerous part of the workflow:

```text
interpretation
  -> planning
  -> generation
  -> deterministic evaluation
  -> bounded retry / replan
  -> sealed candidate
  -> admission
  -> governed apply
  -> verification
```

Sensei Code should orchestrate and present that machinery rather than implement a second synthesis or admission system.

This also updates an early Sensei Dashboard assumption. A worker does not need authority to mutate the canonical checkout merely to produce a candidate. It works in a disposable, pinned candidate workspace. **Admission is required before the sealed candidate becomes an authoritative applied result.**

That boundary keeps experimentation useful without allowing an agent's private workspace to silently become project truth.

---

## Multi-agent orchestration

Sensei Code is provider-neutral at its orchestration layer.

A task may use one worker or several bounded lanes:

```text
                 Architect
                    |
          +---------+---------+
          |                   |
       Claude                Codex
       lane A                lane B
          |                   |
          +---------+---------+
                    |
             candidate review
                    |
            explicit selection
                    |
             Sensei admission
```

Parallel workers never create implicit consensus or authority. Each lane has its own exact candidate identity and evidence. Selecting a candidate for admission must be an explicit, auditable decision.

The simplest workflow remains one architect + one worker + one reviewer. Additional workers are a capability, not a requirement.

---

## Planned command surface

The exact CLI is not frozen yet, but the intended shape is conversational first with explicit control commands:

```text
sensei-code
sensei-code open .
sensei-code resume
sensei-code status
```

Inside the interactive shell:

```text
/help
/task
/status
/sensei
/plan
/agents
/run claude
/run codex
/diff
/evidence
/review
/admit
/apply
/verify
/pr
/resume
```

Most users should not need to memorize the commands. Typing a task in natural language should start the workflow and Sensei Code should stop at the boundaries that require explicit decisions.

---

## Relationship to Sensei Dashboard

Sensei Code carries forward the strongest architectural ideas from the earlier Sensei Dashboard workspace work:

- Sensei core owns architectural truth and admission
- orchestration is local
- agents have explicit roles and capabilities
- workers run in isolated exact-base workspaces
- provider authentication remains provider-owned
- Git/GitHub preserve durable code history
- mutable execution state is separate from architectural projections
- manual and governed sessions remain distinguishable
- the human retains final merge authority

What changes is the product center.

Sensei Dashboard began as an architectural observatory with an AI workspace added beside it. Sensei Code makes the **interactive governed development workflow itself** the primary product surface.

The architecture map and richer visualizations can return later as views over the same canonical Sensei state. They are not prerequisites for the first usable product.

---

## Repository architecture

The planned implementation is a single local Go application with narrow adapters around Sensei, Git, provider runtimes, and optional GitHub integration.

```text
cmd/sensei-code/
internal/
  app/            interactive application state machine
  session/        local resumable UX sessions
  sensei/         typed Sensei client / contract adapters
  architect/      architect runtime abstraction
  provider/       worker provider abstractions and adapters
  review/         reviewer orchestration
  worktree/       exact-base Git workspace management
  git/            Git operations and identity
  github/         optional GitHub publication gateway
  evidence/       presentation/index of Sensei-owned evidence
  ui/             terminal UI
  protocol/       typed events between runtimes and UI
```

See [`docs/architecture.md`](docs/architecture.md) for the governing architecture and lifecycle.

---

## Initial milestones

### M0 — Architecture

- product README
- governing architecture
- authority boundaries
- provider and Sensei integration contracts

### M1 — Local shell and repository readiness

- open repository
- detect Git/Sensei/provider runtimes
- verify Sensei workspace identity and graph readiness
- guide onboarding when a repository is not Sensei-ready
- resumable local session

### M2 — Architect

- OpenAI architect runtime
- persistent task conversation
- Sensei briefing / impact / preflight integration
- explicit architecture proposal and approval boundary

### M3 — Worker lanes

- Claude Code adapter
- Codex adapter
- exact-base isolated workspaces
- normalized agent events
- cancellation and failure handling

### M4 — Governed candidate pipeline

- invoke Sensei synthesis/evaluation machinery
- sealed candidate presentation
- evidence and required-proof views
- bounded retry / replan surfaced honestly

### M5 — Review, admission, apply, verification

- exact-candidate reviewer
- explicit admission request
- exact admitted artifact application
- Sensei verification
- exact-result review

### M6 — GitHub workflow

- issue/PR bindings
- push and draft PR flow
- CI observation
- exact-SHA review and publication receipts

### M7 — Product-quality terminal UX

- polished streaming TUI
- session resume
- multiple worker lanes
- provider capability negotiation
- richer Sensei architecture, evidence, and evolution views

---

## Non-goals

Sensei Code is not:

- a replacement for Sensei
- a second architecture database
- a new model provider
- a credential broker
- an IDE that silently owns the repository
- an excuse to infer PASS from missing evidence
- an autonomous merge bot
- a system where model consensus creates authority

The product can automate more over time, but automation must remain downstream of explicit authority and evidence.

---

## Design law

The central law of Sensei Code is simple:

> **Agents may propose, reason, implement, test, and review. They do not become authoritative merely by participating in the workflow.**

Sensei Code exists to make that disciplined workflow feel as natural as today's best conversational coding tools.

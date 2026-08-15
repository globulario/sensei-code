# Sensei Code

**A uniform surface for your coding agents, with a layer of architectural knowledge underneath.**

Sensei Code does not replace Claude Code, Codex, or Cursor Agent. It gives them the context an agent session cannot give itself — the repository's governed invariants, contracts, failure modes, forbidden fixes, and proof obligations — and it makes the **task**, not the agent session, the unit of continuity.

Switch from one agent to another mid-task and the architectural context, the scope, the decisions already made, and the evidence survive the switch, because they live in Sensei rather than in a transcript.

For changes that warrant it, the same application can run a fully governed pipeline: isolated candidates, deterministic audit, Sensei admission, exact-artifact apply, and verification. That is opt-in, not the default.

It should feel familiar to users of Claude Code, Codex, Cursor Agent, Pi, and similar tools: open a repository, describe a task, watch work happen, inspect decisions, and continue the conversation. The difference is the architectural knowledge underneath, and the fact that Sensei Code does not treat one model session as architect, implementor, reviewer, authority, and memory at the same time.

In **assisted mode**, the default:

```text
        HUMAN + their coding agent
                    |
              SENSEI CODE
     context · task identity · continuity
                    |
                 SENSEI
    invariants | contracts | failure modes
      forbidden fixes | proof obligations
```

In **governed mode**, opt-in per task:

```text
                         HUMAN
                    ultimate authority
                          ^
                          | rare escalation
                          |
                    ARCHITECT / REVIEWER
                         OpenAI
                          ^
                          | architectural authority
                          |
                       SENSEI CODE
                  autonomous orchestrator
                          |
          +---------------+---------------+
          |                               |
       Claude Code                       Codex
        implementor                    implementor
          |                               |
          +---------------+---------------+
                          |
                     Git worktrees
                          |
                        SENSEI
        truth | governance | evidence | admission | closure
```

**Sensei knows and governs. Sensei Code coordinates work. Agents implement.**

> Sensei Code makes Sensei easier to adopt without making Sensei easier to bypass.

## Why it exists

Modern coding agents are powerful implementors, but an agent session does not automatically answer repository-owned questions such as:

- What architectural rules apply to this change?
- Which source is authoritative?
- Which known failure modes and forbidden fixes already exist?
- What scope may change?
- What tests and proof obligations are required?
- Did the candidate stay inside the admitted envelope?
- Is the evidence fresh and bound to the exact result?
- Is the task actually complete?

Sensei already owns those semantics. Sensei Code is the missing workflow layer that lets a developer use them without manually relaying prompts, diffs, receipts, worktrees, and evidence among several terminals.

## Two modes

### Assisted (default)

You drive your agent. Sensei Code supplies the governed architectural context, keeps the task identity, and surfaces what Sensei observes (preflight, edit checks, diff audit) while the work happens. The agent writes in your checkout; you commit. Nothing is sealed and nothing is admitted.

Context is delivered through whatever each agent supports — generated `CLAUDE.md` / `AGENTS.md` / rules files, hook-driven briefings, MCP tools, or a prompt preamble — and it is selected proportionally to the file's rigor class rather than dumped wholesale.

Absence is typed. "No invariants apply here" and "the graph has no coverage here" are different answers, and the second one is never rendered as a blank panel.

### Governed (opt-in)

The autonomous pipeline: isolated candidate worktrees, bounded workers, deterministic diff audit, reviewer cycles, Sensei admission, exact-artifact apply, verification, and completion. It costs more and it buys receipts.

**A task is governed because the canonical Sensei records exist for it — never because a config key says so.** An assisted task is never presented as a governed run.

Neither mode uses Sensei's own code-generation stack. The coding agents generate; Sensei governs. See [`docs/architecture.md`](docs/architecture.md) section 3.4 for the evidence behind that decision.

## Authority, not permission popups

In governed mode, Sensei Code is designed to be autonomous during normal development. It does **not** ask the user whether it may read a file, run a test, create a candidate worktree, or let a bounded worker repair a failed test.

It uses three authority levels:

### Level 1: execution authority

Sensei Code may act autonomously inside its configured local capability envelope. Typical actions include repository inspection, worktree creation, worker execution, builds, tests, Sensei queries, candidate audits, retries, and bounded repair cycles.

### Level 2: architectural authority

The architect may resolve normal architectural questions autonomously, using repository evidence and Sensei context. A worker failure does not become a human question merely because an agent is uncertain.

### Level 3: human authority

Sensei Code interrupts when a decision reaches authority the architect does not own — human-owned product intent, an invariant, an externally meaningful contract, a trust boundary, an explicit policy — **or when Sensei cannot certify the decision at all.**

The trigger is a property of the graph, not a model's confidence. A model deciding whether to bother you is not a control: a confidently wrong architect never escalates, and an uncertain one escalates when nothing is at stake. So the conditions are computable:

```text
the decision touches a region with no graph coverage at this generation
a claim in the governing plan is contradicted by the graph
the governing contract is unknown, contested, or absent
the change would alter human-owned intent, an invariant, or a trust boundary
the graph is not fresh enough to answer the question being decided
Sensei cannot establish who owns the authority for this decision
```

The corollary matters as much: outside those conditions, **do not ask**. A prompt that a certifiable rule could have answered is a defect.

And every answer you give is written back into the graph as an intent, invariant, contract, or forbidden fix — so the same question is certifiable next time and never reaches you again. Without that, the interruption rate plateaus and the system has merely stopped asking rather than learned. With it, your involvement shrinks toward genuinely new territory.

The UI then presents a small numbered decision surface:

```text
╭─ ⚑ HUMAN AUTHORITY REQUIRED ───────────────────────────────────╮
│                                                               │
│ The proposed fix would change a human-owned contract.         │
│                                                               │
│ Architect recommendation: 1                                   │
│                                                               │
│  1. Preserve the contract and redesign the implementation     │
│  2. Change the product policy                                 │
│  3. Stop the task                                              │
│                                                               │
╰───────────────────────────────────────────────────────────────╯
```

Normal execution keeps flowing. Authority crossings do not.

## Current implementation

The first Go foundation is being built around these boundaries:

- Bubble Tea v2 terminal application
- Bubbles v2 textarea input
- Lip Gloss v2 styling
- structured event bus and JSONL session history
- explicit execution / architectural / human authority model
- direct structured MCP connection to Sensei's `awareness-mcp`
- Sensei workspace identity and preflight before architecture
- OpenAI/Codex architect and reviewer adapters
- Claude Code primary worker with Codex fallback
- isolated sibling Git worktrees for candidates
- autonomous bounded review/repair cycles
- Sensei deterministic diff audit before reviewer acceptance
- Level-3 human authority rendezvous and resume
- `sensei-code doctor` readiness checks

The foundation deliberately stops short of pretending that reviewer acceptance is Sensei admission. The next governed slice binds candidate output to Sensei's canonical admission/apply/verification contracts.

See [docs/architecture.md](docs/architecture.md) and [docs/implementation-status.md](docs/implementation-status.md).

## Experience

Assisted mode — the agent is yours, the context is Sensei's:

```text
$ sensei-code

◆ SENSEI
  workspace identity verified · graph generation 2026-08-15T09:14Z (HEAD-2)
  golang/architecture/agentcommand/ · rigor class B
  4 invariants · 2 failure modes · 1 forbidden fix
  proof obligations: api_agent_test.go:TestVendorBoundaryRepair
  runtime coverage: UNAVAILABLE (no crossing source)

● CLAUDE
  working in your checkout...

◆ SENSEI
  edit check: api_agent.go
  ⚑ forbidden fix nearby: "widen the vendor boundary to pass the test"

● CLAUDE
  ...

◆ SENSEI
  diff audit: 2 files · contracts represented · no forbidden fix observed

✓ REVIEWED WORKING TREE
  assisted task · no admission requested · yours to commit
```

Governed mode — opt-in, receipts at the end:

```text
$ sensei-code

◆ SENSEI
  workspace identity verified
  briefing and preflight loaded

◈ ARCHITECT
  architecture resolved
  bounded implementation contract issued

● CLAUDE
  implementing in isolated candidate worktree...

◆ SENSEI
  candidate diff audited
  contracts: represented
  forbidden fixes: none observed

◈ REVIEWER
  REVISE: add the missing clean-room regression test

● CLAUDE
  repairing autonomously...

◆ SENSEI
  candidate diff audited

◈ REVIEWER
  ACCEPT

✓ READY
  candidate ready for governed admission

───────────────────────────────────────────────────────────────
> _
───────────────────────────────────────────────────────────────
ready · agent activity collapsed · Ctrl+O toggle
```

If the architect reaches human-owned authority, the conversation pauses at the numbered decision and resumes the same workflow after the answer.

## Local-first model

The core runtime is local:

```text
Sensei Code
Sensei / awareness-mcp
Git
Codex CLI
Claude Code
compiler / tests / repository tooling
```

GitHub is optional collaboration and publication infrastructure, not project authority.

Model providers own their own authentication. Sensei Code does not collect ChatGPT, Claude, or Cursor credentials.

## Readiness and freshness

The context Sensei Code injects is only as good as the graph behind it, and a stale graph does not fail loudly — it answers confidently and wrongly. Readiness is therefore part of the product, not a setup step.

Every injected packet carries the graph generation it was answered from, and one of:

```text
fresh          graph generation covers HEAD
behind         graph generation predates HEAD by N commits
uncovered      the files in view are not represented at this generation
unbuilt        no graph for this repository/domain
unavailable    the store or service cannot be reached
mismatched     graph identity does not match this checkout
```

Assisted mode shows the state and keeps working. Governed mode fails closed on anything but `fresh` unless the human explicitly accepts a weaker state for that task.

A repository with no graph is the normal first run, not an error. Sensei Code guides the explicit onboarding Sensei owns and never fabricates bindings to reach a green status; until then it degrades honestly to an ordinary agent session with typed-absent context.

`sensei-code doctor` is the single computation behind all of this — binaries, versions, store, generation, freshness, domain, tool subset, provider readiness. The UI reads the same answer the CLI prints.

See [`docs/architecture.md`](docs/architecture.md) section 5, including the operational hazards the product is expected to absorb so that users never meet them.

## Install and build

The shipped artifact is expected to cover the whole runtime — the Sensei Code binary, the Sensei binaries, and the store — so that installing it does not require knowing that a triple store is involved. Provider CLIs are detected, never installed silently.

Sensei Code targets Go 1.25+ because the current Charm v2 packages require that toolchain.

```bash
go build ./cmd/sensei-code
```

Initialize a repository-local configuration:

```bash
sensei-code init
```

Check the execution surface before the first task:

```bash
sensei-code doctor
```

The doctor checks Git, configured model-provider executables, Sensei MCP startup, and the canonical Sensei tools required by the workflow.

Then launch the interactive application from a governed repository:

```bash
sensei-code
```

## Local configuration

Configuration is stored in:

```text
<repository>/.sensei-code/config.json
```

It is local state and should not be committed.

The default capability envelope permits routine local development but refuses external/destructive authority:

```json
{
  "permissions": {
    "read_repository": true,
    "write_candidates": true,
    "create_worktrees": true,
    "run_builds": true,
    "run_tests": true,
    "local_commit": true,
    "push": false,
    "force_push": false,
    "production_deploy": false
  }
}
```

The default provider roles are:

```text
architect:     Codex, read-only repository access
implementor 1: Claude Code, isolated candidate worktree
implementor 2: Codex, isolated candidate worktree fallback
reviewer:      Codex, read-only repository access
```

Provider commands are adapters, not architectural identities. They can be replaced without changing the Sensei-owned governance model.

## Sensei integration

Sensei Code speaks directly to Sensei's structured MCP bridge rather than scraping terminal prose or duplicating Sensei semantics.

The integration surface includes canonical tools such as:

```text
sensei_workspace_status
awareness_briefing
awareness_impact
awareness_preflight
awareness_audit_diff
task_status
task_briefing
advance_task
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

Not every tool is wired into the first vertical slice yet. Sensei remains the owner of those semantics as each stage is added.

## Candidate isolation

Workers never receive the canonical checkout as their normal mutation surface.

Candidate worktrees are created beside the repository:

```text
/work/project/
/work/.project.sensei-code-worktrees/
    task-.../
        claude/
        codex/
```

This separates local application/session state from candidate execution state and reduces the blast radius of autonomous workers.

A candidate worktree is still not authority. It becomes meaningful only through Sensei-governed evaluation, admission, application, verification, and completion.

## Structured events

Provider stdout is normalized into an event stream. The TUI is only one projection of that stream.

Important event classes include:

```text
task.created
agent.started
agent.finished
candidate.changed
candidate.audited
authority.required
authority.resolved
workflow.completed
workflow.failed
```

Raw worker activity is persisted but collapsed in the normal UI. This keeps the interface conversational while preserving evidence for debugging and future JSON/IDE/CI frontends.

## Project rules

1. **Sensei is not an agent.** It is the governance boundary.
2. **Sensei Code does not replace your coding agent.** Assisted mode is the default; the agents keep generating the code.
3. **Mode is derived from receipts, not configuration.** Governed means the canonical Sensei records exist.
4. **Injected context carries its provenance.** Every claim names the graph generation it came from; absence is typed, not blank.
5. **Absence of evidence is not success.** Fail closed when Sensei cannot establish required truth.
6. **Architectural authority extends exactly as far as Sensei can certify it.** Not one decision further.
7. **Escalation is triggered by certifiability, not by model uncertainty.** And do not ask a human what a certifiable rule already answers.
8. **Every human answer becomes a governed entry.** An unrecorded decision guarantees the question returns.
9. **Workers do not own architecture.** They implement bounded contracts.
10. **Reviewer acceptance is not admission.** Sensei owns admission and verification.
11. **Routine execution is autonomous in governed mode.** Do not convert worker uncertainty into human permission prompts.
12. **Human interruption means an authority boundary was reached.** Keep it rare and explicit.
13. **Local UI state is not project truth.** Durable architectural truth belongs to Sensei/repository governance sources.
14. **Manual work must never masquerade as a governed run.** Receipts and exact bindings matter.

## License

See [LICENSE](LICENSE).

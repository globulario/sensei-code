# Sensei Code

**A local-first, governed, autonomous multi-agent development environment powered by Sensei.**

Sensei Code is the terminal application that makes the full Sensei workflow usable as a normal coding experience.

It should feel familiar to users of Claude Code, Codex, Cursor Agent, Pi, and similar tools: open a repository, describe a task, watch work happen, inspect decisions, and continue the conversation. The difference is that Sensei Code does not treat one model session as architect, implementor, reviewer, authority, and memory at the same time.

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

## Authority, not permission popups

Sensei Code is designed to be autonomous during normal development. It does **not** ask the user whether it may read a file, run a test, create a candidate worktree, or let a bounded worker repair a failed test.

It uses three authority levels:

### Level 1: execution authority

Sensei Code may act autonomously inside its configured local capability envelope. Typical actions include repository inspection, worktree creation, worker execution, builds, tests, Sensei queries, candidate audits, retries, and bounded repair cycles.

### Level 2: architectural authority

The architect may resolve normal architectural questions autonomously, using repository evidence and Sensei context. A worker failure does not become a human question merely because an agent is uncertain.

### Level 3: human authority

Sensei Code interrupts only when a decision reaches authority the architect does not own, such as changing human-owned product intent, an invariant, an externally meaningful contract, a trust boundary, or another explicit policy.

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

## Install and build

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
2. **Absence of evidence is not success.** Fail closed when Sensei cannot establish required truth.
3. **Workers do not own architecture.** They implement bounded contracts.
4. **Reviewer acceptance is not admission.** Sensei owns admission and verification.
5. **Routine execution is autonomous.** Do not convert worker uncertainty into human permission prompts.
6. **Human interruption means an authority boundary was reached.** Keep it rare and explicit.
7. **Local UI state is not project truth.** Durable architectural truth belongs to Sensei/repository governance sources.
8. **Manual work must never masquerade as a governed run.** Receipts and exact bindings matter.

## License

See [LICENSE](LICENSE).
